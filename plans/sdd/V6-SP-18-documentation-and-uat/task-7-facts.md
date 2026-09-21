# Facts for the SP-18 delivery record (supplied by the coordinator, 2026-09-14)

Use these verbatim. Where a fact says "from git log", derive it with `git log --format='%h %s' develop..HEAD` at dispatch time (Task 6 may have added a fix commit after this file was written).

## Commits (plan commit → branch commits)

| Plan commit | Branch commits |
|---|---|
| preliminary | 7ace16a docs(plans): record the V6 Opus 4.8-only routing override in SP-18 |
| Commit 1 | 9848254 docs(sp18): establish supported architecture and doc contracts; 1360b6e docs(sp18): narrow README's store claim and complete the ADR table (review fix) |
| prerequisite fix | ddb1a41 fix(devtool): slug MCP tool anchors the way GitHub does (generator bug found by Commit 1's harness: `anchorFor` mapped `_` to `-`; page regenerated; allowlist removed) |
| Commit 2 | 4acc129 feat(devtool): document versioned configuration metadata |
| Commit 3 | 6996795 docs(sp18): explain scoped recovery and command behavior; ce52df3 and 20678d8 (review fixes: checkpoint/preamble/source claims; probed hook write) |
| Commit 4 | a3e15ed docs(sp18): document observable failures and recovery; 8abb7b8 (review fix: citation) |
| Commit 5 | 2bbd441 docs(sp18): state capability limits and upstream proposals; c64f2f5 (review fix: scope contract-absence claims) |
| Commit 6 | 0621f39 docs(sp18): define and execute evidence-based UAT (+ any fix commit from git log) |
| Commit 7 | this commit |

## Routing record

- Coordinator: Fable 5.1 (`claude-fable-5-1`), main session only; never a child.
- Every child (7 implementers incl. resumes, 6 task reviewers, 6 scoped re-reviewers so far, 1 final whole-branch reviewer to come) was dispatched through the harness Agent tool with the alias `opus` and the requested identity/effort stated in its prompt (implementers: `claude-opus-4-8` / high for Commits 1, 2, 6 and medium for 3, 4, 5 per the plan's role table; every reviewer: `claude-opus-4-8` / high).
- Observed: each child reported its own model id as `claude-opus-5[1m]`. The harness exposes no effort control and no way to select `claude-opus-4-8` from a running session (project agent definitions pinned to it, written by the SP-17 session under `.claude/agents/`, return "Agent type not found" in a running session). This is recorded as the explicit R1 fallback, not as Opus 4.8 observed.
- Concurrency: one implementer at a time in one worktree; one reviewer per task; reviewer never the author. Fix rounds: Commit 1 one round, Commit 3 two rounds, Commit 4 one round, Commit 5 one round, Commit 2 none, Commit 6 from git log/ledger. One reviewer stalled (stream watchdog) and was resumed; no result was lost.
- Cross-session incident: the SP-17 session's subagent killed devtool/go/*.test processes machine-wide between 11:22 and 11:36, including this worktree's `lint --only=stubskips` run; re-run, no file damage.

## R2 run map (focused checks first; commands as run; snapshot = the commit they validated)

| Row | Commands | Snapshot | Result |
|---|---|---|---|
| Commit 1 | `go test -count=1 ./test/docs/...` (RED 3 failing on absent pages → GREEN ok), `go vet ./test/docs/...`, `go run ./tools/devtool fmt-check`, `go run ./tools/devtool lint` (all ten sub-checks, ~25 min) | 9848254 | all exit 0; three negative controls confirmed the link/ADR-title checks fail when broken |
| Commit 1 fix | `go test -count=1 ./test/docs/...`, `fmt-check` | 1360b6e | ok / exit 0 |
| prerequisite fix | `go test -count=1 ./tools/devtool/ ./test/docs/...` (RED `TestMCPDocsGlanceLinksResolve` → GREEN), `gen-mcp-docs --check`, `fmt-check` | ddb1a41 | ok; page up to date |
| Commit 2 | `go test -count=1 ./tools/devtool/ ./internal/config/...` (RED → GREEN), `gen-config-docs --check`, `gen-mcp-docs --check`, `gen-command-docs --check`, `fmt-check`, `lint` (golangci-lint, nomagic, importgraph, testdeps, bindeps, sleepcheck PASS; stubskips PASS after ~80 min under co-load — it runs the whole tree's `go test`; runpatterns/docmarkers/coveragefloors read only `plans/`, untouched) | 4acc129 | all exit 0; config page diff 64 insertions, 0 deletions |
| Commit 3 (+2 fixes) | `go test -count=1 ./test/docs/...` (RED 4 → GREEN), `go vet`, `fmt-check`; probes in scratch dirs outside the repo (`status`, `config print`, `version`, `checkpoint </dev/null`), daemons stopped by pid, dirs deleted | 6996795, ce52df3, 20678d8 | ok / exit 0 |
| Commit 4 (+1 fix) | `go test -count=1 ./test/docs/...` (RED 5 → GREEN), `go vet`, `fmt-check`; six one-config-file probes in a scratch dir (found the `LoadForCapture` defect), five daemons stopped by pid, dir and binary deleted | a3e15ed, 8abb7b8 | ok / exit 0 |
| Commit 5 (+1 fix) | `go test -count=1 ./test/docs/...` (RED 4 → GREEN), `go vet`, `fmt-check`; mutation checks on the derivation and §12 tests; no binary run | 2bbd441, c64f2f5 | ok / exit 0 |
| Commit 6 | `go test -count=1 ./test/docs/...` (RED 5 → GREEN), `go vet`, `fmt-check`; three mutation checks; probes `version` and `config print --provenance` only, no daemon, dir and binary deleted | 0621f39 | ok / exit 0 |
| Commit 7 | the brief's validation list | this commit | fill from your run |
| CI | not run on this branch (unpushed; pushing is a separate authorization) | — | no CI result is claimed |
| Baseline | `go build ./...` and `go test -count=1 -timeout=20m ./tools/devtool/ ./internal/config/... ./internal/pluginmanifest/... ./test/guards/` on develop 9c84e31 before any change | 9c84e31 | exit 0 (guards 146 s) |

No timeout, zero-test run or old-tip result is counted as a pass anywhere above. Whole-tree `go test`, race, coverage and replay were not run for prose commits by design (R2); the whole tree did run once inside Commit 2's `stubskips` lint sub-check.

## Gate dispositions

| Gate | Disposition | Evidence |
|---|---|---|
| SP18-M7-01 | partially met — every supported capability, residual and risk named in Qompack.md §12 / V5-report §24/§29 has a user-facing explanation (README, architecture, user-guide, troubleshooting, cannot-do) and each page's claims are test-checked against generated inventories; the requirement-to-doc map is the delivery record's table below; "tested fallback" holds only for behaviours probed or sourced, not for installed-host behaviour (B01) | pages + `test/docs` |
| SP18-M7-02 | met — `docs/config-reference.md` renders every leaf one-to-one from `config.Defaults().JSONSchema()` (`TestGenConfigDocs_LeavesMatchDefaultsOneToOne`), plus origins, versioned blocks, gated switches (pending/passed from `Passed`), build gates and retired-meaning keys from `internal/config`; `--check` detects missing and stale output (`TestGenConfigDocs_CheckDetectsMissingAndStale`); logs in task-2-report.md | 4acc129 |
| SP18-M7-03 | blocked — commands/tools/IDs are matched against the generated pages (which CI diffs against `commands.Specs()` and `mcp.ToolDefs`) and the built binary's `help`, not against an installed package; SP-17's bundle does not exist | user-guide tests; `qompack help` probe |
| SP18-M7-04 | met — `TestRelativeLinksResolve` resolves every relative link and anchor in README and docs/**; the MCP anchor generator bug it found is fixed (ddb1a41); no fixed doc/job counts in tests (the twelve UAT IDs are the plan's fixed inventory, stated in the test comment); planned pages are plain text until they exist and `TestNoPlannedPointerToAnExistingPage` (this commit) enforces conversion | `test/docs` |
| SP18-M7-05 | blocked — no human executed any UAT row; all twelve rows record "not executed — capability unverified" with snapshot/date; ten rows need SP-17's bundle to run | docs/uat.md |
| SP18-M7-06 | met for what exists — backup/switch ladder/rollback are described consistently in troubleshooting §8–§9, config-reference (gated switches, versioned blocks) and uat.md (pre-run copy, restore, verify); the page states that no operator rollback command exists in this build and that `store.NewMigrator` refuses while the legacy-import build gate is closed | troubleshooting §9; uat header |
| SP18-M7-07 | pending the final whole-branch review — every task review (six independent reviewers) checked claims against sources and found no surviving performance/price/native-control/completeness guarantee; the final independent reader review's verdict is recorded in the ledger and appended to this record by the branch's last commit | ledger |

## UAT

All twelve rows drafted with preconditions, steps, expected observable result, evidence fields and failure/rollback; every Result block reads `Result: not executed — capability unverified`, `Snapshot: develop 9c84e31 / branch feat/sp18-documentation-and-uat`, `Date: 2026-09-14`, `Executed by: —`. Ten of twelve rows need SP-17's bundle to run at all. Row-level unknowns recorded: UAT-03's explicit incomplete-outcome API (`ResolveLatest`/`ResolveChain`) has no production caller (probe named in the row); UAT-10: `status` prints no usage categories and `eval` has no artifact seam, so accounting stays unverified.

## Blocked / carried / discovered

- BLOCKED on SP-17 (in progress in a sibling worktree, integrates first): "integrate after SP-17", installed-package claims (SP18-M7-03), human UAT (SP18-M7-05), links to docs/install.md, docs/security.md, docs/release.md (plain text until they exist).
- Discovered defect, routed to V6-VERIFY (owner: internal/config / SP-19 lineage; not fixed by SP-18): `config.LoadForCapture` has no per-leaf fallback (`internal/config/capture_load.go:87-91,111`; `internal/cli/capture_admission.go:111-114`), so an unknown key, a newer `settingsVersion`, an invalid value or a refused gated switch makes every hook admit nothing and create no `.qompack/` while printing `{}` / exit 0, and `self-test` still reports `config.load ok` (soft loader). Six one-config-file probes, troubleshooting §6.
- Fixed on this branch (not carried): MCP tool anchor slugs (ddb1a41).
- Carried observations for V6-VERIFY: `/qompack:checkpoint` unrouted (SP-14 H3); `doctor`, `fsck`, `bench` "not implemented in this build" (SP-17 owns doctor/fsck; V5-report §29 item 7 for bench); the four self-test rows that read `not-yet-implemented` in self-test's zero-Services run; installed-host compatibility `implemented_unverified` (B01).
- Deferred minor review findings (all non-blocking; listed in the ledger for the final review's triage): harness edge cases in `test/docs` (fence toggle, unbalanced backtick, title scrub level, `itoa`), generator hygiene in `genconfigdocs.go` (schema re-parse per gate, silent "" on error, "pending" wording in the gated sentence), prose precision items in user-guide/troubleshooting/upstream-issues named in the ledger.

## Checkboxes to tick in the plan file

Tick: "Seven future conventional commits with no attribution trailers and stable UAT IDs" (seven numbered commits delivered plus review-fix and one prerequisite-fix commit, all conventional, no trailers); "No fixed inventory/leaf/job counts replace current inspected sources"; "Future docs/tests/generators/CI are validated in the implementation session, not this plan pass"; "Config versioning/deprecation and independent switches are correctly described"; "Unsupported native controls and unknown historic/current recovery cases remain explicit"; the R2 run-map exit criterion; the R1 delegation exit criterion (with the routing fallback stated).
Leave unticked with "(blocked: …)": "Independent final reviewer approves user-facing evidence and rollback instructions" (pending the final whole-branch review; tick is made by the branch's last commit if it approves); "SP18-M7-01–07 match actual installed release scope and evidence" (M7-03/05 blocked on SP-17 and a human run); "Future UAT reports include failures/skips, versions, dates and snapshots; no migration gate inferred" (rows drafted with snapshot/date; no report exists until a human run); "SP17 artifacts are integrated before documentation signoff; no fabricated existing files" (SP-17 not landed; no fabricated file — say both halves).

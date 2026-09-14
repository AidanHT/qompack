# SP-18 delivery record (2026-09-14)

The subplan is `plans/V6-SP-18-documentation-and-uat.md`; this file is its delivery record, written
by Commit 7 (`ci(sp18): validate the supported documentation set`) on branch
`feat/sp18-documentation-and-uat`, cut from `develop` 9c84e31.

One thing to read first, because it governs everything below: **SP-17 has not landed.** It is in
progress in a sibling worktree and integrates first. The plan's Commit 7 text says "integrate after
SP17"; that half is recorded here as BLOCKED, not as done. Every claim that needs an installed
package — SP18-M7-03, SP18-M7-05, and the ten UAT rows marked `[requires SP-17 artifact]` — is
blocked with it, and `docs/install.md`, `docs/security.md` and `docs/release.md` stay named as
plain text on the pages that point at them, because they do not exist on this tree.

## 1. Commits

Derived from `git log --format='%h %s' develop..HEAD` at the time this record was written.

| Plan commit | Branch commits |
|---|---|
| preliminary | `7ace16a` docs(plans): record the V6 Opus 4.8-only routing override in SP-18 |
| Commit 1 | `9848254` docs(sp18): establish supported architecture and doc contracts; `1360b6e` docs(sp18): narrow README's store claim and complete the ADR table (review fix) |
| prerequisite fix | `ddb1a41` fix(devtool): slug MCP tool anchors the way GitHub does — a generator bug found by Commit 1's own harness (`anchorFor` mapped `_` to `-`); the page was regenerated and the allowlist removed |
| Commit 2 | `4acc129` feat(devtool): document versioned configuration metadata |
| Commit 3 | `6996795` docs(sp18): explain scoped recovery and command behavior; `ce52df3` docs(sp18): correct the checkpoint, preamble and source claims; `20678d8` docs(sp18): replace the hook no-write claim with a probed result (review fixes) |
| Commit 4 | `a3e15ed` docs(sp18): document observable failures and recovery; `8abb7b8` docs(sp18): attribute the latency-cell quote to render.go (review fix) |
| Commit 5 | `2bbd441` docs(sp18): state capability limits and upstream proposals; `c64f2f5` docs(sp18): scope contract-absence claims to self-test's run (review fix) |
| Commit 6 | `0621f39` docs(sp18): define and execute evidence-based UAT |
| Commit 7 | this commit |

Fourteen commits on the branch: the seven numbered ones, five review fixes, one prerequisite fix
and the preliminary plan revision. All conventional, none carrying an attribution trailer, and all
inside the commit-msg hook's 64-character limit, which it applies to the subject text *after* the
`type(scope): ` prefix (`tools/devtool/checkcommitmsg.go:21`) — two subjects are 66 characters in
total and compliant.

## 2. Routing

- Coordinator: Fable 5.1 (`claude-fable-5-1`), main session only; never a child.
- Every child — 7 implementers including resumes, 7 task reviewers, 6 scoped re-reviewers so far,
  and 1 final whole-branch reviewer — was dispatched through the harness Agent tool
  with the alias `opus` and with the requested identity and effort stated in its prompt
  (implementers: `claude-opus-4-8` / high for Commits 1, 2, 6 and `claude-opus-4-8` / medium for
  Commits 3, 4, 5, per the plan's role table; every reviewer: `claude-opus-4-8` / high).
- Observed: each child reported its own model id as `claude-opus-5[1m]`. The harness exposes no
  effort control and no way to select `claude-opus-4-8` from a running session — project agent
  definitions pinned to it, written by the SP-17 session under `.claude/agents/`, return "Agent type
  not found" in a running session.

  The record therefore reads, for every role: **requested `claude-opus-4-8` / &lt;effort&gt;;
  observed: harness alias `opus`, resolved ID unverified.** This is the explicit fallback R1
  requires, stated as a fallback rather than as a silent substitution. It is not a claim that Opus
  4.8 was observed.
- Concurrency and ownership: one implementer at a time in one worktree; one reviewer per task; the
  reviewer was never the author. Fix rounds: Commit 1 one round, Commit 3 two rounds, Commit 4 one
  round, Commit 5 one round, Commit 2 none, Commit 6 as recorded in the ledger
  (`plans/sdd/V6-SP-18-documentation-and-uat/progress.md`). One reviewer stalled (stream watchdog)
  and was resumed; no result was lost.
- Cross-session incident: the SP-17 session's subagent killed `devtool`, `go` and `*.test` processes
  machine-wide between 11:22 and 11:36, including this worktree's `lint --only=stubskips` run. It
  was re-run; no file damage.

## 3. R2 run map

Focused checks first, as R2 requires; the one broader check is `devtool lint`, and the generator
`--check` runs are the drift gates. The snapshot column is the commit each command validated.

| Row | Commands | Kind | Snapshot | Result |
|---|---|---|---|---|
| Commit 1 | `go test -count=1 ./test/docs/...` (RED 3 failing on absent pages, then GREEN), `go vet ./test/docs/...`, `go run ./tools/devtool fmt-check`, `go run ./tools/devtool lint` (all ten sub-checks, ~25 min) | focused + the one broader check | `9848254` | all exit 0; three negative controls confirmed the link and ADR-title checks fail when broken |
| Commit 1 fix | `go test -count=1 ./test/docs/...`, `fmt-check` | focused | `1360b6e` | ok / exit 0 |
| prerequisite fix | `go test -count=1 ./tools/devtool/ ./test/docs/...` (RED `TestMCPDocsGlanceLinksResolve`, then GREEN), `gen-mcp-docs --check`, `fmt-check` | focused + generator `--check` | `ddb1a41` | ok; page up to date |
| Commit 2 | `go test -count=1 ./tools/devtool/ ./internal/config/...` (RED, then GREEN), `gen-config-docs --check`, `gen-mcp-docs --check`, `gen-command-docs --check`, `fmt-check`, `lint` (golangci-lint, nomagic, importgraph, testdeps, bindeps, sleepcheck PASS; stubskips PASS after ~80 min under co-load — it runs the whole tree's `go test`; runpatterns, docmarkers and coveragefloors read only `plans/`, untouched) | focused + broader + generator `--check` | `4acc129` | all exit 0; config page diff 64 insertions, 0 deletions |
| Commit 3 (+2 fixes) | `go test -count=1 ./test/docs/...` (RED 4, then GREEN), `go vet`, `fmt-check`; probes in scratch dirs outside the repo (`status`, `config print`, `version`, `checkpoint` with empty stdin), daemons stopped by pid, dirs deleted | focused | `6996795`, `ce52df3`, `20678d8` | ok / exit 0 |
| Commit 4 (+1 fix) | `go test -count=1 ./test/docs/...` (RED 5, then GREEN), `go vet`, `fmt-check`; six one-config-file probes in a scratch dir (these found the `LoadForCapture` defect in §6), five daemons stopped by pid, dir and binary deleted | focused | `a3e15ed`, `8abb7b8` | ok / exit 0 |
| Commit 5 (+1 fix) | `go test -count=1 ./test/docs/...` (RED 4, then GREEN), `go vet`, `fmt-check`; mutation checks on the derivation and §12 tests; no binary run | focused | `2bbd441`, `c64f2f5` | ok / exit 0 |
| Commit 6 | `go test -count=1 ./test/docs/...` (RED 5, then GREEN), `go vet`, `fmt-check`; three mutation checks; probes `version` and `config print --provenance` only, no daemon, dir and binary deleted | focused | `0621f39` | ok / exit 0 |
| Commit 7 | `go test -count=1 ./test/docs/...` (RED 7 pointer sites, then GREEN), `go vet ./test/docs/...`, `go run ./tools/devtool fmt-check`, `gen-config-docs --check`, `gen-mcp-docs --check`, `gen-command-docs --check`; the workflow edit parsed with PyYAML | focused + generator `--check` | this commit | all exit 0; output tails in `plans/sdd/V6-SP-18-documentation-and-uat/task-7-report.md` |
| CI | not run on this branch: it is unpushed, and pushing is a separate authorization | — | — | **no CI result is claimed anywhere in this record** |
| Baseline | `go build ./...` and `go test -count=1 -timeout=20m ./tools/devtool/ ./internal/config/... ./internal/pluginmanifest/... ./test/guards/` on `develop` 9c84e31 before any change | focused | `9c84e31` | exit 0 (guards 146 s) |

No timeout, zero-test run or old-tip result is counted as a pass in any row above. Whole-tree
`go test`, the race detector, coverage and replay were deliberately not run for prose commits (R2:
run the checks the change can break); the whole tree did run once, inside Commit 2's `stubskips`
lint sub-check.

## 4. Gate dispositions SP18-M7-01 … 07

| Gate | Disposition | Evidence |
|---|---|---|
| SP18-M7-01 | **partially met** — every supported capability, residual and risk named in `Qompack.md` §12 and `plans/V5-report.md` §24/§29 has a user-facing explanation (README, architecture, user guide, troubleshooting, cannot-do), and each page's claims are test-checked against the generated inventories. The requirement-to-doc map is §5 below. "Tested fallback" holds only for behaviours probed or sourced, not for installed-host behaviour (B01) | the pages + `test/docs` |
| SP18-M7-02 | **met** — `docs/config-reference.md` renders every leaf one-to-one from `config.Defaults().JSONSchema()` (`TestGenConfigDocs_LeavesMatchDefaultsOneToOne`), plus origins, versioned blocks, gated switches (pending/passed from `Passed`), build gates and retired-meaning keys from `internal/config`; `--check` detects missing and stale output (`TestGenConfigDocs_CheckDetectsMissingAndStale`); logs in `plans/sdd/V6-SP-18-documentation-and-uat/task-2-report.md` | `4acc129` |
| SP18-M7-03 | **blocked** — commands, tools and IDs are matched against the generated pages (which CI diffs against `commands.Specs()` and `mcp.ToolDefs`) and against the built binary's `help`, not against an installed package; SP-17's bundle does not exist | user-guide tests; `qompack help` probe |
| SP18-M7-04 | **met** — `TestRelativeLinksResolve` resolves every relative link and anchor in README and `docs/**`; the MCP anchor generator bug it found is fixed (`ddb1a41`); no fixed doc or job counts replace inspected sources (the twelve UAT IDs are the plan's fixed inventory, stated as such in the test comment); planned pages stay plain text until they exist, and `TestNoPlannedPointerToAnExistingPage` — added by this commit — enforces the conversion the moment one lands | `test/docs` |
| SP18-M7-05 | **blocked** — no human executed any UAT row; all twelve record "not executed — capability unverified" with snapshot and date; ten of them need SP-17's bundle to run at all | `docs/uat.md` |
| SP18-M7-06 | **met for what exists** — backup, the switch ladder and rollback are described consistently in troubleshooting §8–§9, in config-reference (gated switches, versioned blocks) and in `uat.md` (pre-run copy, restore, verify); the page states that no operator rollback command exists in this build and that `store.NewMigrator` refuses while the legacy-import build gate is closed | troubleshooting §9; the `uat.md` header |
| SP18-M7-07 | **met** — every task review (seven independent reviewers) checked claims against sources and found no surviving performance, price, native-control or completeness guarantee. The independent whole-branch review then read the branch end to end and returned "With fixes"; its findings are fixed in this commit and its verdict is recorded in the section below | `plans/sdd/V6-SP-18-documentation-and-uat/final-review.md`; the ledger (`plans/sdd/V6-SP-18-documentation-and-uat/progress.md`) |

## 5. Requirement-to-doc map

Where each supported area is explained to a user, and where its limit is stated. Section numbers
are the pages' own headings.

| Area | Explained in | Limit stated in |
|---|---|---|
| Process model, daemon lifecycle | architecture §1; user-guide "Operator commands" | troubleshooting §7; cannot-do §6 |
| Write set, retention, append-only layout | architecture §2 | troubleshooting §8; cannot-do §3 |
| Identity, publication and durability | architecture §3–§4 | troubleshooting §1; cannot-do §2 |
| State authority and uncertainty | architecture §5; user-guide "Current vs historical reads", "Current-authority corrections" | troubleshooting §5; cannot-do §3 |
| Retrieval and its qualifications | architecture §6; user-guide "MCP tools", "Fidelity, coverage and error states" | troubleshooting §4–§5; cannot-do §2 |
| Checkpoint and rehydration | architecture §7 | troubleshooting §3; cannot-do §1 |
| Scheduler, observer, negative knowledge | architecture §8 | cannot-do §4 |
| Historical records vs current amendments | architecture §9 | troubleshooting §5 |
| Configuration, versioning, gated switches | `docs/config-reference.md` (generated); README, "What ships off, and why you should leave it off" | troubleshooting §6; cannot-do §6 |
| Slash commands and MCP tools | `docs/commands.md`, `docs/mcp-tools.md` (generated); user-guide "Slash commands", "MCP tools" | cannot-do §4 |
| Budget, overflow and usage accounting | user-guide "Additional context: budget and overflow", "Usage accounting" | troubleshooting §2; cannot-do §4 |
| Native context control | — (not supported) | architecture §10; cannot-do §1; upstream-issues §7 |
| Model compliance and native-byte proof | — (not supported) | architecture §10; cannot-do §2; upstream-issues §1 |
| Backup, rollback, recovery | troubleshooting §9; `uat.md`, "How to run a scenario" | troubleshooting §9; cannot-do §3 |
| Trust boundary and safe disable | troubleshooting §8; cannot-do §5 | cannot-do §5 |
| Installed-host behaviour, packaging, release | — | README, "Supported environments"; blocked on SP-17 (B01) |

## 6. UAT

All twelve rows (UAT-01 … UAT-12) are drafted with preconditions, steps, an expected observable
result, evidence fields and a failure/rollback outcome. **None was executed.** Every Result block
reads:

```
Result: not executed — capability unverified
Snapshot: develop 9c84e31 / branch feat/sp18-documentation-and-uat
Date: 2026-09-14
Executed by: —
```

Ten of the twelve need SP-17's bundle to run at all; their steps carry `[requires SP-17 artifact]`.
Row-level unknowns recorded on the page itself:

- UAT-03: the explicit incomplete-outcome API (`ResolveLatest` / `ResolveChain`) has no production
  caller; the row names the probe that would show one.
- UAT-10: `status` prints no usage categories and `eval` has no artifact seam, so usage accounting
  stays unverified.

The rollback rule the page uses — the operator's own pre-run copy of `.qompack/` plus an index-file
comparison afterwards — is UAT's interim procedure for a test run. It is not a verified restore and
not the operator procedure planned under SP-17; troubleshooting §9 and `uat.md` now both say so.

## 7. Blocked, carried and discovered

**Blocked on SP-17** (in progress in a sibling worktree; it integrates first):

- "Integrate after SP-17" — recorded as blocked, not claimed.
- Installed-package claims (SP18-M7-03) and human UAT execution (SP18-M7-05).
- Links to `docs/install.md`, `docs/security.md` and `docs/release.md`: plain text until they exist.

**Discovered defect, documented and routed to V6-VERIFY** (owner: `internal/config` / SP-19 lineage;
deliberately not fixed by SP-18, which is a documentation subplan): `config.LoadForCapture` has no
per-leaf fallback (`internal/config/capture_load.go:87-91,111`;
`internal/cli/capture_admission.go:111-114`). One unknown key, a newer `settingsVersion`, one
invalid value or one refused gated switch therefore makes every hook admit nothing and create no
`.qompack/` while printing `{}` and exiting 0 — and `self-test` still reports `config.load ok`,
because it exercises the soft loader. Found by six one-config-file probes in Task 4; written up in
`docs/troubleshooting.md` §6.

**Fixed on this branch, not carried:** the MCP tool anchor slug bug (`ddb1a41`).

**Carried observations for V6-VERIFY:** `/qompack:checkpoint` unrouted (SP-14 H3); `doctor`, `fsck`
and `bench` print "not implemented in this build" (SP-17 owns doctor and fsck; `plans/V5-report.md`
§29 item 7 for bench); the four self-test rows that read `not-yet-implemented` in self-test's
zero-Services run; installed-host compatibility `implemented_unverified` (B01).

**Deferred minor review findings**, all non-blocking and listed in the ledger
(`plans/sdd/V6-SP-18-documentation-and-uat/progress.md`) for the final review's triage: harness
edge cases in `test/docs` (fence toggle, unbalanced backtick, title scrub level, `itoa`); generator hygiene in `genconfigdocs.go` (schema re-parse per gate, silent empty string on
error, the "pending" wording in the gated sentence); and the prose-precision items in the user
guide, troubleshooting and upstream-issues named in the ledger. The final review's triage of this
list is in the section below.

## 8. Checkbox dispositions

Ticked in the plan file (Exit criteria and Done checklist):

- Seven conventional commits with no attribution trailers and stable UAT IDs.
- No fixed inventory, leaf or job counts replace currently inspected sources.
- Docs, tests, generators and CI are validated in the implementation session.
- Config versioning/deprecation and independent switches are correctly described.
- Unsupported native controls and unknown historic/current recovery cases remain explicit.
- The R2 run-map exit criterion (§3 above).
- The R1 delegation exit criterion, with the routing fallback stated (§2 above).
- Commit-plan boxes 1–7, Commit 7's annotated "(SP-17 integration BLOCKED; docs checks wired)".
- Independent final reviewer approves user-facing evidence and rollback instructions — ticked by
  this commit, which lands the review's fixes; the verdict and the fix/deferred split are the
  section below.

The CI half of "Docs, tests, generators and CI are validated in the implementation session" is
annotated in place: the docs checks, tests and generators were validated locally, but the CI step
itself is unexercised because the branch is unpushed.

Left unticked, each annotated in place with its blocker:

- SP18-M7-01–07 match actual installed release scope and evidence — M7-03 and M7-05 are blocked on
  SP-17's artifact and on an authorized human run.
- Future UAT reports include failures/skips, versions, dates and snapshots — the rows are drafted
  with snapshot and date, but no report exists until a human runs them.
- SP-17 artifacts are integrated before documentation signoff; no fabricated existing files — SP-17
  has not landed, and no file was fabricated: every page named as a link exists on this tree, and
  every page that does not exist is named as plain text. Two tests keep that honest, one direction
  each: `TestRelativeLinksResolve` fails on a link whose target is missing, and
  `TestNoPlannedPointerToAnExistingPage` fails on a "planned" pointer whose target now exists.

## Independent final review (2026-09-14)

An independent whole-branch reviewer — a fresh child that authored none of the branch — read
9c84e31..dca2146 end to end and returned **"With fixes"**. Its report is
`plans/sdd/V6-SP-18-documentation-and-uat/final-review.md`.

**Fixed in this commit** (the four Important findings, plus four Minor findings folded in because
each is a one-line edit):

| # | Finding | Fix |
|---|---|---|
| Important 1 | `docs/troubleshooting.md` §7 attributed a planned operator-facing stop path to SP-17; SP-17's plan makes no such commitment | the sentence now reads "No subplan currently owns an operator-facing stop path" |
| Important 2 | §1 called `config-violations.json` the record of "every leaf that fell back to its default", but a whole-block `settingsVersion` reset surfaces only as a day-log warning and section-level fallbacks are not decoded into the file (`internal/config/validate.go:481-483`) | "every leaf-level fallback", with the whole-block case named and linked to §6 |
| Important 3 | `docs/user-guide.md` called two separate probes "the same probe" | "observed in a separate probe on this tree" |
| Important 4 | the plan's "Future docs/tests/generators/CI are validated in the implementation session" box was ticked with no caveat while CI never ran on this branch | annotated "(docs/tests/generators validated locally; the CI step itself is unexercised — the branch is unpushed)" |
| Minor 5 | `docs/config-reference.md`'s generated "Two blocks carry their own `settingsVersion`" states a count above a derived table | the generator now emits "The blocks below carry their own `settingsVersion`"; the page was regenerated and `gen-config-docs --check` passes |
| Minor 6 | `docs/architecture.md:47` "a hook has 15 ms at p99" reads as a measurement | "a hook has a 15 ms p99 budget (B-A)" |
| Minor 8 | the plan's Commit-plan checkboxes 1–7 were still `[ ]` while the Done checklist ticked seven commits | all seven ticked; Commit 7's carries "(SP-17 integration BLOCKED; docs checks wired)" |
| Minor 10 | §2 said "6 task reviewers" for seven tasks, and §1's 64-character sentence was loose about where the limit applies | "7 task reviewers"; §1 now states that the limit applies to the subject text after the `type(scope): ` prefix (`tools/devtool/checkcommitmsg.go:21`) |

**Deferred** (recorded, not fixed):

- Minor 7 — no `test/docs` check pins README's gated-switch table (`README.md:112-126`) or its "seven
  hooks" count (`README.md:77`, `docs/architecture.md:15`) to the generated and source inventories;
  a one-line check would close the gap.
- Minor 9 — `README.md:3-15` states injection as a flat capability, and the "installed behavior
  unknown" qualifier arrives at `README.md:50-55`; a half-clause at first mention would make the
  opening self-qualifying.

**The reviewer's triage of the ledger's deferred minors.** Three are fix-before-merge, and all three
are already covered by Important 1–3 above (the user guide's "same probe"; the violations-file
overstatement; the SP-17 stop-path attribution). The `bench`-label item is already resolved — no
`SP-05` remains in the page. Every other ledger item may stay deferred: latent `test/docs` harness
edge cases, generator hygiene in `genconfigdocs.go`, prose precision and formatting.

**Contradictions returned to the coordinator: none.** The reviewer found no rollback, privacy or
capability contradiction across the five surfaces it checked (troubleshooting §8–§9, the
config-reference gated and versioned sections, `uat.md`'s rollback rule and UAT-11/12, cannot-do's
trust boundary and §5, architecture's write-set section); they tell one story.

**Reviewer routing.** Requested `claude-opus-4-8` / high; dispatched through the harness alias
`opus`; the child observed its own model id as `claude-opus-5[1m]` — R1's documented fallback shape,
stated as a fallback rather than as a silent substitution.

**Still blocked, and not changed by this review:** SP-17's bundle, an authorized human UAT run and
the integration order. They gate the signoff, not this review.

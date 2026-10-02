> **Superseded for candidate 6, 2026-10-02.** The gates below were listed against `65bc8d7`. Candidate
> 6 (`verify/v6` `99d0b18`) has run them: the Windows whole tree, `-race`, isolated timing and
> reproducible six-target bundles in its overnight chain (`../V6-closeout/phase3/c6/`), and hosted
> ci.yml `36955046276` and nightly `36955043924` on the same head. The inventory's per-row result is in
> `inventory-current.tsv` (`c6_result`, `c6_evidence`) and `../V6-closeout/inventory-c6-map.md`, which
> lists the open reds (TestFault_Lifecycle on hosted Windows, X11, release-dry-run), the quiet C5.1
> result and the pending live and evaluation steps. This page is kept as history.

> Coordinator execution update, 2026-09-22: `rc1-linux-child-race` passed all eight
> selected cases on candidate source 65bc8d7. Its executed-source record verifies all 2,376
> source files; child SHA256 is `5b0151bac4771ecb16bd78815465673e4951004f829d3f2f3d20fea3d6d4842d`.
> This resolves the earlier split-snapshot coverage caveat, not hosted CI or installed-host UAT.
> `rc1-bundle-identities/verification.json` records two actual, reproducible builds per target
> (Windows/amd64 and Linux/amd64). Windows Claude Code 2.1.263 accepted strict directory validation.
> `rc1-deterministic-replay` passed the existing 24-session synthetic phase-0 comparison;
> modeled latency and fraction-of-OPT remain synthetic diagnostics, not live task/cost outcomes.
> The whole-tree gate is still running and has already failed two daemon fixtures; focused
> corrections pass separately. No overall candidate PASS or human UAT completion is asserted.

# V6 current-candidate gate map — remaining gates against `65bc8d7`

Read-only mapping. **No test/build/generator/benchmark/installer/probe was executed here; no source,
git, release or config change was made.** Canonical author route verified `claude-opus-4-8`.
`V6-report.md`, `plans/sdd/V6-VERIFY/`, and every older report/map/baseline are immutable and
untouched. This map **supersedes `automated-closeout-run-map.md`** (which was written against the
older, still-uncommitted `c95b7af` snapshot) without rewriting it. All **304** inventory IDs are
preserved in `inventory-current.tsv`; this map re-statuses none of them — it names the remaining
GATES, their prerequisites, candidate identity, and which owner implements a missing entry point.
Historical run artifacts are diagnostic, never a new PASS.

## Candidate identity

- **Source candidate:** committed HEAD **`65bc8d7`** (`git describe` = `v0.2.0-670-g65bc8d7`),
  branch `verify/v6`; the detached `qompack-v6-rc1` checkout is **clean** — every remediation family that the inventory
  body once carried as `WORKING-TREE`/`(M)` has LANDED as a commit (landing map in
  `inventory-current-notes.md`). Committing did **not** promote any row: all landed rows stay
  `implemented_unverified`, none is `verified_in_target`.
- **Final integrated run is PENDING.** A whole-tree `go run ./tools/devtool test` is executing in the
  detached sibling worktree `qompack-v6-rc1`; its result is not yet in. The Windows/amd64 and Linux/amd64 candidate bundles are now frozen and identified in
  `rc1-bundle-identities/verification.json`. Their source is `65bc8d7`; no tag or public release
  version is selected. The main integration worktree holds new evidence and two fixture corrections,
  and must not be described as clean.

## Key blockers (must clear before any final PASS)

1. **Final integrated run incomplete; preserve its observed failures.** The source is committed and clean, but the
   integrated whole-tree gate on `65bc8d7` is still running in `qompack-v6-rc1`; two daemon fixture
   failures are recorded and the focused corrections pass in the integration worktree. Both retained
   candidate archives were built twice and are byte-identical. → Finish the suite and inspect every
   failure, skip and per-package artifact. Evidence/report commits must not silently
   re-identify the tested source.
2. **Version identity disagreement (release blocker).** `internal/core/version.go` `Version = "0.1.0"`;
   last release tag `v0.2.0`; `git describe` = `v0.2.0-670-g65bc8d7`. **No V6 release version has been
   chosen.** `release-check` **without `--tag`** SKIPS the version-agreement step and can still run
   its other gates; `release-check --tag vX.Y.Z` **additionally** requires the tag's version to equal
   `core.Version` at the exact HEAD, so a tagged run FAILS until an owner picks the version and sets
   `core.Version` to match. Not automatable — owner decision.
3. **CI on the candidate has not run; the only recorded run is historical.** GitHub nightly
   `35704111100` ran on historical source `9c84e31d` (26 success / 3 failure): a real Linux **and**
   Windows **B-B budget** failure, and Windows **race** job failed with two permission-fixture assertions and one
   concurrent-write deadline. That is not the candidate. `docs/release.md §7` has been narrowed (accepted docs
   handoff) to "the release workflow is unverified in the available evidence" with this historical-CI evidence; it no longer
   claims "no workflow ever ran." Inventory `1.17.19`'s required jobs on `65bc8d7` remain unmet →
   needs a hosted runner (GitHub billing/runner is an environmental dependency).
4. **Product-child race gate is now WIRED but not run on hosted CI.** `8948992` set
   `QOMPACK_REQUIRE_CHILD_RACE=1` in nightly (`.github/workflows/nightly.yml:117`, `race-windows`),
   and `test/e2e/race_instrumentation_test.go`'s `TestE2E_RequiredProductChildRaceInstrumentation`
   verifies the built child carries `-race`. **Local snapshot** evidence: the B7 cases pass and CX10
   passes; the `child-race-workflow-guards-integrated` guard run passed. (An earlier isolated Docker
   run `linux-product-child-race-corrected` FAILED on `36205d1`, i.e. before the MCP-diagnostics/exited-child
   sync `108bd63` and Linux-lock reclaim `5c8329e`.) **Final candidate / hosted-CI run is pending.**
5. **Non-source obligations remain** (see §C). V6 is not complete with only human tests remaining —
   but two obligations formerly filed as "human" are **implementation/evaluation** obligations, not
   inherently human (§C notes).

## A. Automated gates (runnable on a supported runner, no human) — against frozen `65bc8d7`

Do NOT rerun the whole tree per inventory row; run the integrated gate once (in progress in
`qompack-v6-rc1`), then targeted reruns only for real failures. Packaged suites assemble a bundle via
`go run ./tools/devtool bundle`.

| gate | scope / note | owner |
|---|---|---|
| Integrated whole-tree | `GOMAXPROCS=2 go test ./... -timeout=30m`. **Name the structural-guard tests explicitly** — the guard executes benchmarks and hit the 10-min timeout when run whole; the immutable `V6-report.md` unresolved-carry result must remain, not be suppressed. Inspect artifact outcomes, not only Go exit codes. | repo/CI |
| Packaged fault (F4-1/F4-4) | `go test ./test/fault`. Publication accounting (`80a3e04`/`7e5a141`) is **detection, not recovery**; `1.17.15` stays `implemented_unverified` until this passes on `65bc8d7`. | test/fault |
| Packaged security (M7-03, §3.11) | `go test ./test/security`. Historical **FAILED** (`posture_out_of_project_capture_is_archived`); V6-AUTH fixes `c9b5251`/`00e0c98` landed → re-run **owed**; `1.13.4`/`1.17.12` stay FAILED-preserved until then. Host native-Read parity is NOT covered here (§C HOST). | test/security |
| Packaged e2e/canary/platform/release/docs/integration | `go test ./test/e2e ./test/canary ./test/platform ./test/release ./test/docs ./test/integration`. Covers §3.1–3.14 source-level rows and M7-01/02/06. `[installed-host]`/other-OS rows stay NOT-RUN (env/human). | respective pkg |
| Product-child race (blocker 4) | `CGO_ENABLED=1 GOFLAGS=-race QOMPACK_REQUIRE_CHILD_RACE=1 go test ./test/e2e -run TestE2E_RequiredProductChildRaceInstrumentation -count=1` on a supported CGO platform; already wired into nightly — run on the candidate and record child sha256 + go version. | test/e2e + CI |
| Maintenance/backup/rollback (M7-05, §3.9) | `go test ./internal/store ./internal/cli -run 'TestMaintenance\|TestRollbackDrill\|TestBackup\|TestBackupCLI'`. Same-build point-in-time restore only; `AutomaticDowngrade` always false. **Old-released-reader compatibility drills are AUTOMATED and still OWED** — not inherently human-only (§C reclassifies). | store/cli |
| Reproducible bundle / determinism (§3.13) | `go test ./tools/devtool ./internal/pluginmanifest -run 'AssembleBundle_Deterministic\|_ChecksumsFormat\|WriteArchiveChecksums\|ReleaseCheckDeterminismVersion\|Bundle_OnDiskMatchesGenerator'`. Real two-build reproducibility is a build-matrix step, not this unit. | devtool |
| Generated-doc drift | `gen-config-docs --check` · `gen-command-docs --check` · `gen-mcp-docs --check` · `licenses --check`; `go test ./test/docs`. Check mode preserves generated files; drift remains a gate failure. `test/docs` passed in the earlier identified documentation handoff; the candidate suite is separate. | devtool/docs |
| Eval accounting (M7-07, automated part) | `qompack eval --json` currently exits 1 ("no evaluation artifacts are readable"). The nil `eval.LiveRunner` is a **missing evaluation entry point — an implementation/evaluation obligation, not inherently a human action** (§C EVAL). | eval |
| Quiet performance (§3.14) | `bench-gate` / nightly `bench-deep` on a **quiet dedicated runner**. The historical co-loaded/hosted B-B budget failures are retained; a later quiet run has its own identity and does not erase them (no post-outcome threshold change; hosted-runner fsync tail is a known artifact, owner Q1). SP20-D6 pre-ACK re-measure (`c78f610` correction) owed. | perf/CI |
| Release-check (M7-08) | `go run ./tools/devtool release-check [--tag vX.Y.Z] [--skip-vulncheck]` → `dist/release-check.json`. **No-tag** run skips version agreement and exercises ci-local + build-all + govulncheck + import allow-list + generated-doc checks; **`--tag`** adds the exact-HEAD tag==`core.Version` requirement (blocker 2). Registry-name/tag availability is NOT checked here (§C NAME). | devtool/release |
| Linux isolated snapshot | run the focused/whole gate inside the identified Go 1.26.6 container on `65bc8d7`; qualify Docker/WSL as an environmental dependency and retain snapshot identity. | CI/infra |

**Reusable focused evidence (do NOT re-import as a bundle PASS):** `capacity-integration-rollover-*`
(rollover source is default-off, NOT accepted — a focused pass is not enablement),
`segment-offline-reader-*`, `gc-r3-*`, and the accepted rows in `continuation-status.md`. Each names
its own source HEAD in its `.source.json`; a bundle gate must re-run against `65bc8d7`.

## B. CI obligations (1.17.19 — none have executed on the candidate)

`.github/workflows/`: **ci.yml** (verify, lint-windows, test, test-e2e, timing, cover, crossbuild,
bench-gate, replay-gate, plugin-validate, security, docs, release-dry-run); **nightly.yml** (fuzz
matrix, `race-windows` plus the separate `race-product-child` job, `bench-deep`, replay-recorded);
**release.yml** (tag-triggered). Actions: (1) trigger ci.yml on frozen `65bc8d7`, record run id +
per-job outcomes (needs a runner); (2) confirm the child-race env runs and records child identity;
(3) do not tag until blocker 2 is reconciled and ci.yml is green; (4) confirm no required nightly
fuzz target is a stub (`test/guards TestNightlyFuzzMatrix`).

## C. Human & environment / implementation dependencies (not runnable here)

| obligation | inventory / row | classification |
|---|---|---|
| **Human UAT-01…12** | SP18-M7-05 / `1.18.12` | **Human.** Every `docs/uat.md` Result reads "not executed — capability unverified"; a human on the supported installed artifact. Automated packaged calls do not substitute. |
| **Held-out / changing-requirement model eval** | SP17-M7-07 live layer | **Implementation + model session.** `eval.LiveRunner` nil is a missing evaluation entry point (an implementation/evaluation obligation), then a real permitted model session with a declared task set, repeated baseline, model identity, completion/constraint outcomes and usage completeness. Not inherently a human keystroke. |
| **Old-released-reader compatibility drills** | §3.9, `1.17.7/8/9`, docs/backup | **Automated, still owed.** Cross-version reader drills against an older released binary are scriptable; they are owed, not inherently human-only. Same-build restore ≠ old-release compatibility. |
| **macOS/darwin + windows/arm64 installed-host** | §3.1/3.3/3.9/3.10/3.11/3.12 `[installed-host]`, M7-01/02/06 | **Environment.** Only `windows/amd64` has SP17 installed records; others are cross-compile-only (`unknown`). Needs per-OS runners + an installed host. |
| **Registry-name / tag availability (NAME)** | M7-08 | **Environment/decision.** Public-name lookups were performed 2026-09-22, but there is **no registrability guarantee**; `release-check` does not query a registry. |
| **Independent human reader review** | SP18-M7-07 | **Human.** Source-level doc claims are clean; the external human review is the open item. |
| **Host native-Read authorization parity (HOST)** | §3.11, docs/security §1/§8 | **Shared-trust decision — returns to Main.** Project containment + capture-time policy is not parity with a live/managed/CLI host permission decision; the pending USER preference on supported retrieval scope is Main's to answer. |

## D. Carried inventory scope (preserved, not merged into the 304)

- **304 inventory IDs** (`1.1.1`–`1.18.14`), one TSV row each; five rows reconciled off stale
  working-tree language (`1.5.7`, `1.5.12`, `1.8.6`, `1.17.9`, `1.17.15`); zero dropped, zero
  renumbered.
- **14 §3 integration identifiers** (§4 of the notes); genuine gaps stay **packaged-bundle-live** and
  **human/held-out evaluation**.
- **SP19/20/21 migration additions** — nine switches, all shipped **disabled/refused-until-gate or
  hardwired-off**, recorded disabled, never "passed"; admission (`replacement.newResult`) off →
  pass-through. **Delivery-journal rollover (`enableDeliveryGenerations=false`, default-off) is NOT
  accepted — a mandatory capacity-carry-blocker, not a silent waiver.**
- **12 human UAT** (`1.18.12`, UAT-01…12) — human-unexecuted.
- **Three evaluation layers** — accounting (automated, `qompack eval` exits 1 today), live/held-out
  (implementation + model session), and usage-completeness (no summing of overlapping resumed usage).
- **Remaining gates:** packaging, privacy, recovery, performance, and independent acceptance — all
  open; none certified against an installed/packaged target (`verified_in_target` = **none**).

## Owner actions

1. Land any remaining author/reviewer handoffs, capture the integrated `qompack-v6-rc1` result on
   `65bc8d7`, then **freeze** and record HEAD + per-target bundle hashes (blocker 1).
2. Release owner: choose the V6 version, reconcile `core.Version` ↔ tag (blocker 2); only then
   `release-check --tag`.
3. CI/repo owner: first ci.yml run on `65bc8d7`; confirm the wired child-race job runs and records
   child identity (blockers 3–4).
4. Run §A once on the frozen HEAD; targeted reruns only for real failures; keep every skip/disabled/
   failed state and platform/binary identity (no fabricated PASS).
5. Route §C to real permitted sessions/runners/owners; **shared-trust host-permission and any other
   trust/contract questions return to Main.** No release/tag/publish/merge-to-develop until §A green +
   blockers cleared + §C dispositioned.

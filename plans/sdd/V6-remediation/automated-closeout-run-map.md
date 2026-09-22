# V6 automated close-out run map — remaining gates main can execute

Read-only inventory. **No test/build/generator/benchmark/installer/probe was executed; no source, git,
release or config change was made.** Canonical author route verified `claude-opus-4-8`. `V6-report.md`,
`plans/sdd/V6-VERIFY/`, and every older report/map are immutable and untouched. All **304** original
inventory IDs are preserved (see `inventory-current.tsv`); this map re-statuses none of them — it names
the remaining GATES, their exact commands/prerequisites, reusable artifacts, candidate identity, and
which owner implements a missing entry point. Historical run artifacts are diagnostic, never a new PASS.

## Key blockers (must clear before any final PASS)

1. **Source is not frozen.** GC (`gc_delivery_segments.go`) and offline-reader handoffs, the producer
   review, and journal restart/crash + archived-reader integration are still in flight; rollover stays
   default-off pending these gates. No final candidate/package exists. → Freeze after author/reviewer
   handoffs in conventional commits (all source/test/launcher/schema/doc changes), record ONE source
   HEAD, then build once per supported target/mode and record bundle identity + hashes. Every gate below
   runs against that frozen HEAD; evidence/report commits must not silently re-identify the tested source.
2. **Version identity disagreement (release blocker).** `internal/core/version.go` `Version = "0.1.0"`;
   last tag `v0.2.0`; `git describe` = `v0.2.0-660-g06681ef`. `release-check --tag` REQUIRES the tag's
   version to equal `core.Version`, so it FAILS today for any of those. → Owner decision: pick the V6
   release version, set `core.Version` to match the intended tag, then run the gate. Not automatable.
3. **No CI workflow has ever run** (`docs/release.md §7`). Inventory 1.17.19's required jobs are unmet.
   → Needs a CI runner (GitHub billing/runner is an environmental dependency).
4. **Product-child race gate exists but is unwired and unrun.** `test/e2e/race_instrumentation_test.go`
   (`TestE2E_RequiredProductChildRaceInstrumentation`) skips unless `QOMPACK_REQUIRE_CHILD_RACE=1`; no
   workflow sets it. Nightly `race-windows` only `-race`-instruments the TEST process and excludes
   `test/e2e`, so the actual product child is not covered. → Run it (below) AND wire it into CI.
5. **Human/model/native/registry obligations remain and are NOT automatable** (see §"Human & environment
   dependencies"). V6 is not complete with only human tests remaining.

## A. Automated gates (runnable on an available supported runner, no human)

Prerequisite for all: the frozen HEAD (blocker 1). Packaged suites assemble a bundle via
`go run ./tools/devtool bundle`. Do NOT rerun the whole tree per inventory row; run the integrated gate
once, then targeted reruns only for actual failures (verification-protocol §Whole-tree).

| gate | exact command | prerequisite / note | reuse / owner |
|---|---|---|---|
| Integrated whole-tree | `GOMAXPROCS=2 go test ./... -timeout=30m` | Focused prerequisites green first. Inspect artifact outcomes, not only Go exit codes. **Name the structural-guard tests explicitly** — the guard executes benchmarks and hit the 10 min timeout when run whole; the immutable `V6-report.md` unresolved-carry result must remain, not be suppressed. | owner: repo/CI |
| Packaged fault (F4-1/F4-4 cuts) | `GOMAXPROCS=2 go test ./test/fault -timeout=30m` | Publication accounting (`80a3e04`) is **detection, not recovery**; row stays `implemented_unverified` until this packaged run passes on the frozen HEAD. | owner: test/fault |
| Packaged security (M7-03, 3.11) | `go test ./test/security` | Historical **FAILED** (`posture_out_of_project_capture_is_archived`); V6-AUTH fix `c9b5251` → re-run owed. Host native-Read parity is NOT covered here (human, §HOST). | owner: test/security |
| Packaged e2e / canary / platform / release / docs / integration | `go test ./test/e2e ./test/canary ./test/platform ./test/release ./test/docs ./test/integration` | Covers M7-01/02/06, 3.1–3.14 source-level rows. `[installed-host]`/other-OS rows stay NOT-RUN (env/human). | owner: respective test pkg |
| **Product-child race** (NEW) | `CGO_ENABLED=1 GOFLAGS=-race QOMPACK_REQUIRE_CHILD_RACE=1 go test ./test/e2e -run TestE2E_RequiredProductChildRaceInstrumentation -count=1` | One supported local platform with a C toolchain (Linux or Windows+CGO). Verifies the built child carries `-race`; records child sha256+go version. **Also wire into nightly/CI** (a job that sets the three env vars). | owner: test/e2e + CI |
| Maintenance / backup / rollback (M7-05, 3.9) | `go test ./internal/store ./internal/cli -run 'TestMaintenance|TestRollbackDrill|TestBackup|TestBackupCLI'` | Same-build point-in-time restore only; old-release/post-new-write downgrade stays gated closed (`AutomaticDowngrade` always false). Old-released-reader matrix = env/human. | owner: store/cli |
| Reproducible bundle / determinism (3.13) | `go test ./tools/devtool ./internal/pluginmanifest -run 'AssembleBundle_Deterministic|_ChecksumsFormat|WriteArchiveChecksums|ReleaseCheckDeterminismVersion|Bundle_OnDiskMatchesGenerator'` | No `devtool package` cmd exists; the assembler is the entry point. Real-binary reproducibility across two builds = a build-matrix step, not this unit. | owner: devtool |
| Generated-doc drift | `go run ./tools/devtool gen-config-docs --check` · `gen-command-docs` · `gen-mcp-docs` · `licenses`; `go test ./test/docs` | `licenses` regenerates `THIRD_PARTY_NOTICES.md`; a diff there is a real gate. | owner: devtool/docs |
| Eval accounting (M7-07, automated part only) | `qompack eval --json` | Currently exits 1 ("no evaluation artifacts are readable"). The **live/held-out layer is deliberately excluded** (`eval.LiveRunner` nil, ruling R7-2) → that layer is a model obligation (§EVAL), not this command. | owner: eval |
| Quiet performance (3.14, perf) | `bench-gate` / nightly `bench-deep` on a **quiet dedicated runner** | The co-loaded B-F failure is retained; a later quiet run has its own identity and does not erase it — no post-outcome threshold change. SP20-D6 pre-ACK re-measure (`c78f610`) owed. | owner: perf/CI |
| Release-check gate (M7-08) | `go run ./tools/devtool release-check --tag vX.Y.Z [--skip-vulncheck] [--evidence-copy PATH]` → `dist/release-check.json` | **Blocked by blocker 2** (tag==`core.Version`). Steps: ci-local sequence + build-all + govulncheck + import allow-list + generated-doc checks + licences. `--skip-vulncheck` only for an offline machine (records SKIPPED); CI never passes it. **Registry-name/tag availability is NOT checked here** (§NAME). | owner: devtool/release |
| Linux isolated snapshot | run the focused/whole gate inside `golang:1.26.6-bookworm` (`sha256:116d58cb…c6f36`) on the frozen snapshot | Docker/WSL is an environmental dependency; qualify it and retain snapshot identity. | owner: CI/infra |

**Reusable focused evidence (do NOT re-run; they are source-level diagnostics on their own HEAD, and do
not certify the frozen bundle):** `capacity-integration-rollover-final` (≥70 live rotations),
`segment-offline-reader-03` (offline reader), `gc-r3-*` (GC), the accepted focused rows in
`continuation-status.md`. Each names its source HEAD in its `.source.json`; a bundle gate must re-run
against the frozen HEAD, not import these.

## B. CI obligations (1.17.19 — none have executed)

`.github/workflows/`: **ci.yml** (jobs: verify, lint-windows, test, test-e2e, timing, cover, crossbuild,
bench-gate, replay-gate, plugin-validate, security, docs, release-dry-run) on the frozen commit;
**nightly.yml** (fuzz matrix, race-windows, bench-deep, replay-recorded); **release.yml** (tag-triggered
→ `release-check --tag $GITHUB_REF_NAME`). Actions:
1. Trigger ci.yml on the frozen HEAD and record the run id + per-job outcomes (needs a runner/billing).
2. Add the product-child race env (blocker 4) to a job (nightly `race-windows` or a new `child-race` job).
3. `release.yml` only fires on a tag push and depends on blocker 2 — do not tag until the version is
   reconciled and ci.yml is green.
4. Nightly fuzz matrix warns for not-yet-landed targets (`test/guards TestNightlyFuzzMatrix`); confirm no
   required target is a stub before claiming fuzz coverage.

## C. Human & environment dependencies (NOT automatable — real permitted sessions/runners)

| obligation | inventory / row | why it cannot be automated here |
|---|---|---|
| **Human UAT-01…12** | SP18-M7-05 / 1.18.12 | Every `docs/uat.md` Result reads "not executed — capability unverified"; requires a human on the supported installed artifact. Automated packaged calls do not substitute. |
| **Held-out / changing-requirement model eval** | SP17-M7-07 live layer | `eval.LiveRunner` is nil by design; needs a declared task set, repeated baseline, model identity, completion/constraint outcomes, and usage completeness in a real permitted model session. |
| **macOS/darwin + windows/arm64 installed-host** | 3.1/3.3/3.9/3.10/3.11/3.12 `[installed-host]`, M7-01/02/06 | Only `windows/amd64` has SP17 installed records; others are cross-compile-only (`unknown`). Needs macOS/ARM runners + an installed Claude Code host per OS. Cross-compilation is not installed-target proof. |
| **Registry-name / tag availability (§NAME)** | M7-08 | No web primary-source lookup performed; name/registry availability is **unverified**. `release-check` does not query a registry. |
| **Independent human reader review** | SP18-M7-07 | Doc claims are clean at source level; the external human review is the open item. |
| **Host native-Read authorization parity (§HOST, V6-HOST-1)** | 3.11, docs/security §1/§8 | Project containment + capture-time policy is not parity with a live/managed/CLI host permission decision. A pending USER preference on supported retrieval scope must be answered first; neither branch counts as native-policy proof. |

## D. Candidate identity & snapshot scope

- **Candidate identity needed:** one frozen commit SHA + `core.Version` reconciled to the intended tag +
  per-target bundle sha256 (windows/amd64 real; linux/darwin cross-compiled and labelled as such). No
  older source (e.g. `301a8e9` reference binary `8cb4c0bc…6bd0`, reporting `v0.2.0-640-g301a8e9`) and no
  temporary/dirty package certifies the final installed bundle.
- **Supported snapshot scope:** automated gates + Linux container + windows/amd64 packaged. Out of scope
  for this build without human/hardware: macOS, windows/arm64 installed-host, human UAT, live model eval,
  registry lookup, host-permission parity.

## Key actions (owner → do)

1. Producer/GC/offline-reader/reviewer seats → land handoffs; then **freeze** (blocker 1) and record HEAD.
2. Release owner → reconcile `core.Version` ↔ tag (blocker 2); only then `release-check --tag`.
3. CI/repo owner → first ci.yml run on the frozen HEAD; wire the product-child race env (blockers 3–4).
4. Run §A automated gates once on the frozen HEAD; targeted reruns only for real failures; keep artifact
   outcomes, sample counts, platform/binary identity, and every skip/disabled/failed state (no fake pass).
5. Route §C obligations to their real permitted sessions/runners; record each unavailable target
   explicitly. No release/tag/publish/merge-to-develop until §A green + blockers cleared + §C dispositioned.

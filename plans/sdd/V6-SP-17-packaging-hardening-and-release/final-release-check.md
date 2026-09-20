# SP-17 final verification — the release gate on the integrated tree

This is the record of the mandatory final gate for `plans/V6-SP-17-packaging-hardening-and-release.md`:
`devtool release-check` executed ONCE, alone on the machine, over the integrated Task 6 + Task 7 + Task 8
tree, with its summary committed beside this file as `release-check.json`. That record — not this prose —
is what `devtool release-scope` reads to raise SP17-M7-07 and SP17-M7-08.

## 1. Tested tree

| | |
|---|---|
| branch | `feat/sp17-packaging-hardening-and-release` |
| commit | `4db5cea` (`test(paths): cover the restore, create-once and atomic write refusals`) |
| history since Commit 5 | `2c21467` → `ada54d1` (Commit 6, hardening, one squashed commit) → `62bd384` (Commit 7, release gating) → `4f71152` (Commit 8, install rehearsal) → `e62c0b1`, `5bc53a0`, `f574baf`, `108da5b`, `5a33a92`, `b77cedf`, `cb397c0`, `cf58edb`, `4db5cea` (review fix rounds, follow-ups and the two gate-found fixes below) |
| working tree | clean (`git status --short` empty) before the run |
| host | Windows 11 amd64, Go 1.26.6, `claude` 2.1.263 on PATH |
| machine state | no other test, build or benchmark workload for the whole run (the recorded co-load ruling) |
| command | `go run ./tools/devtool release-check --evidence-copy plans/sdd/V6-SP-17-packaging-hardening-and-release/release-check.json` |
| flags | no `--tag` (`core.Version` is `0.1.0` and no tag names it — ruling R7-3; the release owner bumps and tags deliberately); vulncheck enabled; no `QOMPACK_*` variable set in the invoking shell |
| wall | 2 h 21 min (15:53:58 → 18:14:50, 2026-09-16) |
| exit | **0** — "17 step(s), all PASS or SKIPPED" |

## 2. The seventeen steps

| # | step | status | seconds | what it established |
|---|---|---|---|---|
| 1 | version agreement | SKIPPED | 0 | no tag: the tag/`core.Version`/`git describe` agreement is gated at tag time (R7-3) |
| 2 | ci-local fmt-check | PASS | 4.7 | gofumpt clean |
| 3 | ci-local lint | PASS | 1418 | golangci-lint, nomagic, importgraph, testdeps, bindeps, sleepcheck, **stubskips** (whole-tree, run alone), runpatterns, docmarkers, coveragefloors |
| 4 | ci-local vet | PASS | 3.2 | `go vet ./...` |
| 5 | ci-local build | PASS | 2.3 | host binary |
| 6 | ci-local test | PASS | 3032 | whole-tree `go test -timeout=30m ./...` under `QOMPACK_UNDER_COLOAD=1`; every package `ok` (e2e 1255 s, integration 756 s, store 508 s, mcp 399 s, fault 327 s, observer 264 s, daemon 277 s, guards 238 s) |
| 7 | ci-local cover | PASS | 2484 | whole-tree `-coverprofile -covermode=atomic`; every `plans/OWNERS.tsv` floor met on the merged profile — notably `store` 90.5% (floor 90), `paths` 91.2% (floor 90), `config` 91.3%, `checkpoint` 91.0%, `cli` 79.0% (floor 75) |
| 8 | ci-local plugin-validate | PASS | 0.3 | committed `plugin/` equals the manifest generator's output |
| 9 | ci-local gen-config-docs | PASS | 0.1 | `docs/config-reference.md` current |
| 10 | build-all | PASS | 130 | all six release targets cross-compile |
| 11 | generated docs | PASS | 0.1 | `docs/mcp-tools.md`, `docs/commands.md` current |
| 12 | guards | PASS | 296 | `go test -count=1 ./test/guards/` — workflow pins, co-load policy, no-network, goreleaser/release contract |
| 13 | govulncheck | PASS | 13.7 | "Your code is affected by 0 vulnerabilities" (1 in an imported package and 3 in required modules are not reached by this code) |
| 14 | licenses | PASS | 3.7 | `THIRD_PARTY_NOTICES.md` matches the dependency graph |
| 15 | real-binary determinism | PASS | 994 | two independent windows/amd64 assemblies at `v0.2.0-619-g4db5cea` byte-identical (version from `resolveVersion()`, no tag) |
| 16 | rollback rehearsal | PASS | 69 | `-list`-confirmed then run: `./internal/store/` × 3 (drill before/after the first new-format write, restored backup opens as a real store, tamper detection) and `./test/e2e/` × 3 (`TestInstall_HostCLIInstallUpgradeUninstall`, `TestRollbackRehearsal_BeforeAndAfterTheFirstNewFormatWrite`, `TestUnknownSchema_NewerThanThisBuildDegradesWithoutRewriting`) |
| 17 | plugin-validate | PASS | 0.0 | repeated after the assemblies |

## 3. The two earlier attempts, preserved

The gate was run three times; the first two stopped and each stop produced a fix commit. Neither failing run
is erased by the passing one.

| attempt | commit | stopped at | cause | fix |
|---|---|---|---|---|
| 1 (13:53–14:10) | `b77cedf` | step 3 `ci-local lint` | `golangci-lint` reported 15 findings in Task 5/6 code (errcheck on unchecked type assertions ×8, unconvert ×2, gocritic unlambda ×2, misspell ×2 on `proj\other` in comments, staticcheck SA4006 ×1) — every task seat had linted with `--only=` subsets that exclude golangci-lint | `cb397c0` `fix(lint): satisfy golangci-lint across the hardening and diagnostics code` (no behaviour change) |
| 2 (14:21–15:36) | `cf58edb` | step 7 `ci-local cover` | steps 1–6 passed (whole-tree test green); `internal/paths` measured 89.2% against its 90 floor after round 2 added `paths.RestoreLog` and the `resolveLinks` worklist with untested error branches; every other floor passed | `4db5cea` `test(paths): cover the restore, create-once and atomic write refusals` — twelve behaviour tests (missing parent, non-log extension, directory at the path, unstatable path, non-regular entry, read-only destination replaced, staging cleaned on failure, well-terminated tail unchanged, hop bound fails closed); paths 89.2% → 91.2% |
| 3 (15:53–18:14) | `4db5cea` | — | all 17 steps PASS or SKIPPED | this record |

The failed summaries (`ok:false`) were moved out of the evidence root so that `release-scope` never reads a
failing run as SP17-M7-07's record; their full console logs are retained in the coordinator's scratch
directory and summarised here.

## 4. Supported scope after this gate

`go run ./tools/devtool release-scope --markdown` on `4db5cea` with `release-check.json` present:

| target | status | artifact |
|---|---|---|
| `windows/amd64` | installed-verified | `commit8-install-windows-amd64/install_launcher_resolves_in_cache.json` |
| `linux/amd64`, `linux/arm64`, `darwin/amd64`, `darwin/arm64`, `windows/arm64` | unknown | — (they cross-compile in step 10 and in CI's `crossbuild`; a compile leaves no record and is not promoted to support) |

| row | status | artifact / reason |
|---|---|---|
| SP17-M7-01 | verified | installed-CLI record (`claude` 2.1.263, user scope, cache checksums, launcher sha256) |
| SP17-M7-02 | verified | 17 platform records, 17 verified |
| SP17-M7-03 | **unverified** | `posture_out_of_project_capture_is_archived` reports `failed` — finding S-6, ruled: no capture-time refusal; the boundary is retrieval-time authorization; documented in `docs/security.md` §8 |
| SP17-M7-04 | **unverified** | `capture_sidecar_stage_one_only` reports `failed` — finding F4-4, ruled: `qompack fsck` is the surface; two further fault rows stay `failed` for the same reason (`object_written_index_line_absent` F4-1, `historical_checkpoint_drops_pointer` F4-1 mirror shape) |
| SP17-M7-05 | verified | rollback records: before/after the first new-format write, sealed-checkpoint restore (D8-1 fixed), restored root read through the installed launcher |
| SP17-M7-06 | verified | switch matrix (7 records) + uninstall (2) + unknown_schema (4) records |
| SP17-M7-07 | verified | `release-check.json` (`ok:true`, no FAIL step) — this gate |
| SP17-M7-08 | verified | `release-check.json` + verified rollback records, no failed rollback record |
| SP17-M7-07 / live-task layer | excluded | no live model runs are executed by this repository's tests (R7-2) |

`release-scope` also prints one stderr line for `commit3-security-windows-amd64/privacy-surface-sweep.json`
(a Task 3 sweep document with no `outcome`); it raises nothing and is reported rather than dropped silently.

## 5. What this gate does not establish

- **No publication action was taken.** No tag was pushed, no workflow ran, no draft release exists, goreleaser
  was never invoked, no provenance attestation was produced, nothing was installed into the user's live
  Claude configuration. `release.yml` and `.goreleaser.yaml` are verified by shape (`test/guards/release_test.go`)
  and by the local `bundle --archive` path only; the first tag push is the first run of the publisher.
- **Version agreement is untested until a tag exists.** `core.Version` is `0.1.0`; the newest tag is
  `v0.2.0`. Step 1 is SKIPPED here by design and FAILS on a tag that does not equal `core.Version`.
- **One host.** Every installed-target record is windows/amd64 with `claude` 2.1.263. The other five targets
  are compiled, not installed; the table says `unknown` and the release ships them as untested at the
  deployment level.
- **Two acceptance rows are honestly `unverified`** (SP17-M7-03, SP17-M7-04): each is pinned by a record whose
  `failed` outcome is a ruled, documented behaviour with a named surface, not an open defect nobody looked at.
- **Residuals returned to owners, not fixed here** (all recorded in `commit6-evidence.md` §7): the F4-9
  false-orphan window in the checkpoint integrity sweep; a newer versioned config block that also carries an
  unknown key still refuses on the capture path (Loud, recorded) while `Load` drops the key; `AppendFileVersion`
  does not sort in-memory history; negknow blind mode is not observable through the seam (R5-3); `ReadLock`
  cannot tell a live from a stale holder on Windows (R5-4).
- **No timing or storage-ratio claim** is made anywhere in this record.

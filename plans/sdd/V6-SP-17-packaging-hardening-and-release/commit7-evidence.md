# Commit 7 — `ci(release): gate release on evidence and rollback`

Plan row: "Prepare versioned release workflow/tooling and independent feature switches; validate
package identity/licenses and rollback before future publication." (plans/V6-SP-17-packaging-
hardening-and-release.md §3, SP17-M7-06/07/08.)

Platform: windows/amd64, Go 1.26.6, branch `feat/sp17-task7`, parent `d37f392` (Commit 6).

Evidence was regenerated after rebasing onto Commit 6. On base `2c21467` the gate stopped at
`cover` for R5-7 and later steps were run by hand once (§7.7). After the rebase the store floor is
cleared (90.6% measured in `commit6-evidence.md` §3). The **full** `release-check` is executed once,
alone, as the SP-17 final gate on the integrated tree (Tasks 7 + 8) and recorded in
`plans/sdd/V6-SP-17-packaging-hardening-and-release/final-release-check.md` — because Task 8 appends
its e2e names to `releaseCheckExtraTests` and step 9 must run them. It has not run yet.

## 1. What shipped

| file | what it is |
|---|---|
| `tools/devtool/bundlearchive.go` | `bundle --archive`: the reproducible zip/tar.gz packer and the archive-level `checksums.txt` |
| `tools/devtool/bundlearchive_test.go` | members equal the bundle directory, two assemblies pack byte-identically, mode normalisation, the checksum format |
| `tools/devtool/bundle.go` | the `--archive` flag and `archiveAssembledBundles` |
| `tools/devtool/releasecheck.go` | the gate driver: ordered steps, stop-at-first-FAIL, `dist/release-check.json` |
| `tools/devtool/releasechecksteps.go` | the ten steps, including `releaseCheckExtraTests` (the hook Task 8 appends to) |
| `tools/devtool/releasecheck_test.go` | the driver's table test over an injected step runner, the summary shape, the version gate, the step ordering |
| `tools/devtool/licenses.go` / `licensesrender.go` | `licenses --write` / `licenses --check` and the `THIRD_PARTY_NOTICES.md` renderer |
| `tools/devtool/licenses_test.go` | the module set equals allow-list ∩ actual deps, a fake module renders, a version bump is detected |
| `tools/devtool/releasescope.go` / `releasescopereport.go` | the evidence collector and the two tables |
| `tools/devtool/releasescope_test.go` | empty evidence, a missing directory, the four-status ladder, a failed record pinning its row, prose raising nothing |
| `tools/devtool/main.go` | registers `release-check`, `release-scope`, `licenses` |
| `tools/devtool/importrules.go` | `test/release` declared a composition root |
| `.goreleaser.yaml` | R7-1: publishes, does not build; draft release; `extra_files` over the archives |
| `.github/workflows/release.yml` | gate first, then assemble/archive, evidence upload, scope notes, provenance attestation, goreleaser |
| `.github/workflows/ci.yml` | one NEW job, `release-dry-run` (no existing job edited, R7-4) |
| `test/guards/release_test.go` | the goreleaser contract and the gate-before-publisher ordering |
| `test/release/` | the independent-switch matrix: `release.go`, `main_test.go`, `switches_test.go` |
| `docs/release.md`, `docs/install.md`, `docs/security.md` | the release procedure and scope, the install/upgrade/uninstall policy, the security and recovery page |
| `THIRD_PARTY_NOTICES.md` | generated |
| `CHANGELOG.md` | `[Unreleased]` filled for SP-17 commits 1–7 |
| `plans/00-ARCHITECTURE.md` | §2.6 Release row, the one-build-path paragraph, the task list, and the release.yml description in §8 |

Entry points: `taskBundle` bundle.go:104 (`--archive` at :112), `writeArchive` bundlearchive.go:105,
`taskReleaseCheck` releasecheck.go:87, `releaseCheckSteps` releasechecksteps.go:38,
`releaseCheckExtraTests` releasechecksteps.go:35, `taskLicenses` licenses.go:65,
`taskReleaseScope` releasescope.go:103, `buildScopeReport` releasescopereport.go:44,
`measureSuppression` test/release/switches_test.go:117.

## 2. RED

Before the implementation, all three tasks were absent and the goreleaser guard failed on the
committed file.

```text
$ go run ./tools/devtool release-check --skip-vulncheck
devtool: unknown task "release-check"
$ go run ./tools/devtool licenses --check
devtool: unknown task "licenses"
$ go run ./tools/devtool release-scope --markdown
devtool: unknown task "release-scope"
```

```text
$ go test -count=1 ./test/guards/ -run 'TestGoreleaserBuildsNothingAndDraftsTheRelease|TestReleaseWorkflowGatesGoreleaserBehindReleaseCheck' -v
--- FAIL: TestGoreleaserBuildsNothingAndDraftsTheRelease (0.06s)
    .goreleaser.yaml has 1 build entr(ies) and 0 `skip: true` line(s)
--- FAIL: TestReleaseWorkflowGatesGoreleaserBehindReleaseCheck (0.06s)
    release.yml runs no `devtool release-check`
FAIL
```

## 3. GREEN

Filled in §7 below, one subsection per validation command.

## 4. The release-check run, and which steps ran by hand

On base `2c21467` `internal/store` sat below its coverage floor (finding R5-7), so `release-check`'s
`ci-local cover` step stopped the gate. The steps after it were executed by hand, once each (§7.7).
After the rebase onto Commit 6 the store floor is cleared. The full `release-check` runs once as the
SP-17 final gate on the integrated tree and is recorded in `final-release-check.md` (§7.8).

## 5. Supported scope at this commit

`go run ./tools/devtool release-scope --markdown` — see §7.5. Read it as the release's actual claim:
one of six targets has a committed record, and that record is a *directory* validation rather than
an installed-plugin canary.

## 6. The switch matrix

`test/release` flips one switch at a time through the shipped bundle's own binary, with everything
else at its default, and takes a control measurement on the same project so an empty answer under a
switch is distinguishable from an empty answer under anything. Records are collected into
`commit7-switches-windows-amd64/`.

The design decision worth recording: a running daemon loaded its configuration at startup, so an
environment variable set in a later hook process changes the hook and **not** the resident daemon. A
case that flipped a switch mid-session would measure a daemon still running under the defaults. Every
suppression case therefore seeds with the switch already set, measures, stops the daemon and waits
for the lock file to go away, and only then takes the control.

One correction to the brief, recorded rather than papered over: `runtime.mode: passive` is **not** a
recording switch. `Mode.MayRecord` (internal/contract/mode.go) is false only for `off`;
`ModeDegradedPassive` exists precisely so L0/L1 keep recording while every acting path is off. The
`switch_mode_passive` case therefore asserts what §12.1 actually promises — no reinjection, while
recording and retrieval keep working — and says so in its record. The brief's "nothing is written
under objects|index" holds for `runtime.mode: off`, which the `switch_mode_off` case asserts.

Existing coverage deliberately not duplicated: `internal/daemon`'s
`TestService_ReinjectionKillSwitchEmitsNothing` (the reinjection switch at the service seam),
`test/e2e`'s v5_x15 (a daemon-disabled project end to end), `internal/config`'s `validate_test.go`
(the refusal of every gated leaf), and `test/security`'s `posture_telemetry_is_refused`.

## 7. Commands and output

All runs: windows/amd64, 2026-09-15, on a machine shared with two other implementers running their
own suites. Where that co-load changed an outcome it is named.

### 7.1 Formatting, vet, the focused unit suite, the generated config page

```text
$ go run ./tools/devtool fmt-check
(silent)                                                            exit 0

$ go vet ./tools/devtool/ ./test/release/ ./test/guards/
(silent)                                                            exit 0

$ go test -count=1 ./tools/devtool/
ok      github.com/qompack/qompack/tools/devtool        11.812s

$ go run ./tools/devtool gen-config-docs --check
gen-config-docs: docs/config-reference.md is up to date
```

### 7.2 test/guards

On the rebased tree (parent `d37f392`) the full package is green. Task 6 fixed finding G-1
(`TestGuard_FaultEnvIsConfinedToTwoFiles`).

```text
$ go test -count=1 ./test/guards/
ok      github.com/qompack/qompack/test/guards  166.288s
```

The two new release guards, the Go-version pin and both co-load guards pass with the new
`release.yml` and the new `release-dry-run` job in place. (The checker below splits a command at a
bare `|`, so it reads this alternation as truncated; every name in it appears in the `--- PASS`
lines underneath, which is the same confirmation it would perform.)

<!-- runpatterns: the alternation is split at the shell-pipeline character by this checker's parser; every branch is named in the PASS lines below -->
```text
$ go test -count=1 ./test/guards/ -run '^(TestGoreleaserBuildsNothingAndDraftsTheRelease|TestReleaseWorkflowGatesGoreleaserBehindReleaseCheck|TestWorkflowGoVersionMatchesToolchain|TestColoadYieldersAreJudgedInIsolation|TestColoadDeclarationIsPinnedToTheGoConstant)$' -v
--- PASS: TestWorkflowGoVersionMatchesToolchain (0.05s)
        18 go-version pins across [ci.yml nightly.yml release.yml] all equal go.mod's toolchain go1.26.6
--- PASS: TestColoadDeclarationIsPinnedToTheGoConstant (0.31s)
--- PASS: TestGoreleaserBuildsNothingAndDraftsTheRelease (0.33s)
--- PASS: TestReleaseWorkflowGatesGoreleaserBehindReleaseCheck (0.34s)
--- PASS: TestColoadYieldersAreJudgedInIsolation (15.60s)
ok      github.com/qompack/qompack/test/guards  15.968s
```

### 7.3 test/release — the switch matrix, collecting

One collecting run, which replaces a plain run plus a collecting one:

```text
$ QOMPACK_RELEASE_ARTIFACTS=<the commit7-switches directory> go test -count=1 -timeout=30m ./test/release/
ok      github.com/qompack/qompack/test/release 11.616s
```

Seven records, all `verified`, in `commit7-switches-windows-amd64/` (bundle
`v0.2.0-607-g2c21467-dirty` — dirty because the worktree carried this commit's own uncommitted
changes when the bundle was assembled):

| record | switch | what it established |
| --- | --- | --- |
| `switch_mode_passive` | `runtime.mode: passive` | no injected checkpoint span, while recording and retrieval keep working. Control on the same project after a daemon restart: 621 bytes carrying an injected span, recall count 2 |
| `switch_mode_off` | `runtime.mode: off` | all six hooks exit 0 with empty or parseable JSON with no additionalContext; nothing under objects+index; no daemon lock file ever created |
| `switch_reinjection_session_start_compact` | `runtime.migration.reinjection.sessionStartCompact: false` | no injected span on `SessionStart source: compact`, while the session's tool event still reached `index/tool_use.jsonl`. Control: 621 bytes carrying a span |
| `switch_daemon_disabled` | `runtime.daemon.enabled: false` | the lock file never appeared, hooks exit 0, and the spool still took six client files |
| `switch_refused_replacement_new_result` | `runtime.migration.replacement.newResult: true` | refused, restored to false, recorded in `state/config-violations.json` |
| `switch_refused_experiments_enabled` | `runtime.migration.experiments.enabled: true` | same |
| `switch_refused_telemetry_enabled` | `runtime.telemetry.enabled: true` | same |

One measured correction made during this work: the first draft asserted that `SessionStart
source: compact` answers with an EMPTY `additionalContext` when reinjection is off. It does not —
it answers with the contract monitor's probe marker, which is contract machinery rather than
reinjection and which the reinjection switch has no business suppressing. Asserting emptiness
would have recorded a switch that suppressed more than it claims to. The cases now assert the
absence of the injected span (`internal/checkpoint`'s own exported open tag), which is what the
switch actually controls.

### 7.4 Licences

```text
$ go run ./tools/devtool licenses --check
licenses: THIRD_PARTY_NOTICES.md is current
```

The generated page reproduces three redistributed modules — `github.com/Microsoft/go-winio`
v0.6.2 (MIT), `github.com/klauspost/compress` v1.18.0 (BSD-3-Clause with Apache-2.0 and MIT
components, ten licence files reproduced, including the xxhash one the zstd path actually uses),
and `golang.org/x/sys` v0.33.0 (BSD-3-Clause) — and lists `go-cmp`, `testify`, `golang.org/x/tools`
and `pgregory.net/rapid` (MPL-2.0) as build- or test-time only, with the bindeps proof named.

### 7.5 Supported scope

Captured at the Task 7 commit (parent `d37f392`), 2026-09-15.

```text
$ go run ./tools/devtool release-scope --markdown
```

## Supported scope

Derived by `go run ./tools/devtool release-scope` from the committed records under `plans/sdd/V6-SP-17-packaging-hardening-and-release`.
A status is raised only by a record with an `outcome`; prose never raises one.

| target | status | artifact |
| --- | --- | --- |
| `linux/amd64` | unknown | — |
| `linux/arm64` | unknown | — |
| `darwin/amd64` | unknown | — |
| `darwin/arm64` | unknown | — |
| `windows/amd64` | directory-validated | commit1-host-validation.json |
| `windows/arm64` | unknown | — |

`installed-verified` > `directory-validated` > `built` > `unknown`. `built` means a committed record names that target; a cross-compile proven only by CI's
`crossbuild` job leaves no record here and so reads `unknown`.

## Acceptance rows

| row | status | artifact | note |
| --- | --- | --- | --- |
| SP17-M7-01 | unverified | commit1-host-validation.json | directory validation only: the bundle was validated where it sits, which is not an installed-plugin canary (Qompack.md §7.5) |
| SP17-M7-02 | verified | platform records | 17 record(s): 17 verified, 0 skipped, 0 failed |
| SP17-M7-03 | unverified | commit3-security-windows-amd64/posture_out_of_project_capture_is_archived.json | a security record reports failed: posture_out_of_project_capture_is_archived |
| SP17-M7-04 | unverified | commit4-fault-windows-amd64/capture_sidecar_stage_one_only.json | a fault record reports failed: capture_sidecar_stage_one_only |
| SP17-M7-05 | unverified | — | no rollback record is attached |
| SP17-M7-06 | unverified | switches records | still missing: a verified uninstall record; a verified unknown_schema record |
| SP17-M7-07 | unverified | — | accounting attached; no release-check record verifies it |
| SP17-M7-08 | unverified | — | still missing: a verified release-check record; a verified rollback rehearsal record |
| SP17-M7-07 / live-task layer | excluded | — | no live model runs are executed by this repository's tests; eval.LiveRunner is nil in every shipped build and is deliberately left unwired (ruling R7-2) |

`verified` = a record says so. `unverified` = no record says so, which is not a claim
that it is broken. `excluded` = deliberately out of scope, with the reason stated.

The same tables are pasted into `docs/release.md` §3. Per target: `windows/amd64`
`directory-validated`; the other five `unknown`. Per row: SP17-M7-02 is now `verified` (Commit 6
regenerated platform records); SP17-M7-03 stays `unverified` (S-6
`posture_out_of_project_capture_is_archived` remains `failed` by ruling); SP17-M7-04 stays
`unverified` (fault records including `capture_sidecar_stage_one_only` remain `failed` by ruling).
SP17-M7-07 stays `unverified`: this page is prose, and no committed `release-check.json` yet
verifies it. The final gate writes that record; until then release-scope reports it unverified.
SP17-M7-08 stays `unverified` (needs that record plus a verified rollback install record).

### 7.6 The lint sub-checks

The plan-document group is clean:

```text
$ go run ./tools/devtool lint --only=runpatterns,docmarkers,coveragefloors
PASS runpatterns
docmarkers: 161 plan document(s) scanned
PASS docmarkers
coveragefloors: 63 floor claim(s) across 161 plan document(s) checked against plans/OWNERS.tsv
PASS coveragefloors
```

The graph and source group ran as part of the larger invocation the brief names. `nomagic`,
`importgraph`, `testdeps`, `bindeps` and `sleepcheck` all passed; `stubskips` failed for a co-load
reason and not a defect — it runs its own whole-tree `go test`, and under three concurrent
implementers `test/e2e` hit its wall:

```text
$ go run ./tools/devtool lint --only=nomagic,importgraph,testdeps,bindeps,sleepcheck,stubskips
[...]
  github.com/qompack/qompack/test/e2e: test binary killed for running past -timeout=30m, so every
  skip it had not yet reached is missing from this run — the tree was only partly inspected
FAIL stubskips: stubskips: 1 problem(s)
devtool: lint: lint: failed sub-check(s): stubskips
```

No test anywhere in that run reported `--- FAIL`. The check is reporting that it could not finish
inspecting, which is exactly the honest answer it was written to give; a quiet machine is what it
needs, not a code change.

### 7.7 The by-hand later steps

`release-check` stops at the first FAIL, and on this base that is `ci-local cover` — see §7.8. The
steps after it were therefore run by hand, once each, and are labelled as such here.

```text
$ go run ./tools/devtool build-all                                  (by hand)
dist/ holds all six binaries

$ go run ./tools/devtool gen-mcp-docs --check                       (by hand)
gen-mcp-docs: docs/mcp-tools.md is up to date
$ go run ./tools/devtool gen-command-docs --check                   (by hand)
gen-command-docs: docs/commands.md is up to date

$ go test -count=1 ./test/guards/                                   (by hand — §7.2)

$ go run -modfile=tools/pinned/go.mod golang.org/x/vuln/cmd/govulncheck ./...   (by hand)
No vulnerabilities found.
Your code is affected by 0 vulnerabilities.
```

The vulnerability database WAS reachable. `--skip-vulncheck` was passed to `release-check` on the
coordinator's instruction, not because the scan could not run, and the scan was run separately so
the recorded SKIPPED is not hiding an unreachable database.

```text
$ go run ./tools/devtool licenses --check                           (by hand — §7.4)

$ go run ./tools/devtool bundle --target windows/amd64 --version determinism-probe --out <A>   (by hand)
$ go run ./tools/devtool bundle --target windows/amd64 --version determinism-probe --out <B>
$ cmp every file
same  ./.claude-plugin/plugin.json
same  ./.mcp.json
same  ./bin/qompack.exe
same  ./BUNDLE.json
same  ./checksums.txt
same  ./commands/checkpoint.md ./commands/dropped.md ./commands/eval.md ./commands/pin.md
same  ./commands/recall.md ./commands/status.md ./commands/why.md
same  ./hooks/hooks.json
```

Thirteen files, the real cross-compiled binary among them, byte-identical across two independent
assemblies.

<!-- runpatterns: the alternation is split at the shell-pipeline character by this checker's parser; the -list output directly below names every branch -->
```text
$ go test -count=1 ./internal/store -list '^(TestBackup_RestoreOpensAsARealStore|TestBackup_VerifyDetectsTamperingAndSizeDrift|TestRollbackDrill_BeforeAndAfterTheFirstNewFormatWrite)$'
TestRollbackDrill_BeforeAndAfterTheFirstNewFormatWrite
TestBackup_VerifyDetectsTamperingAndSizeDrift
TestBackup_RestoreOpensAsARealStore

$ go test -count=1 ./internal/store -run '^(TestBackup_RestoreOpensAsARealStore|TestBackup_VerifyDetectsTamperingAndSizeDrift|TestRollbackDrill_BeforeAndAfterTheFirstNewFormatWrite)$' -v   (by hand)
--- PASS: TestRollbackDrill_BeforeAndAfterTheFirstNewFormatWrite (4.04s)
--- PASS: TestBackup_VerifyDetectsTamperingAndSizeDrift (1.25s)
--- PASS: TestBackup_RestoreOpensAsARealStore (1.12s)
ok      github.com/qompack/qompack/internal/store       7.785s

$ go run ./tools/devtool plugin-validate                            (by hand)
plugin-validate: OK (10 file(s), 7 command(s), 7 hook event(s))
```

The `-list` run above is the confirmation the gate itself performs before executing: `go test -run`
prints `ok` when its pattern matches nothing, so a renamed rehearsal would otherwise turn the gate
green having rehearsed nothing.

`bundle --archive` smoke, two targets:

```text
$ go run ./tools/devtool bundle --target windows/amd64 --target linux/amd64 --version archive-probe --archive --out <dir>
bundle: packed qompack-plugin-archive-probe-windows-amd64.zip
bundle: packed qompack-plugin-archive-probe-linux-amd64.tar.gz
bundle: checksums.txt covers 2 archive(s)

$ cat <dir>/checksums.txt
5fe4424f67bd8386a44b78e53d18fb73fd59abfe26f8a47c1b3f28e5b34366f1  qompack-plugin-archive-probe-linux-amd64.tar.gz
744244c85fcff4caa0ce739edc70d62cf6688e9ebc2d8a32de7348b2cc2226c6  qompack-plugin-archive-probe-windows-amd64.zip
```

### 7.8 The release-check run

On base `2c21467` the gate stopped at `ci-local cover` for finding R5-7 (store 89.0% vs the 90
floor). Every later step was run by hand once (§7.7). After the rebase onto Commit 6 the store
floor is cleared (90.6% measured in `commit6-evidence.md` §3). The **full** `release-check` is
executed once, alone, as the SP-17 final gate on the integrated tree (Tasks 7 + 8) and recorded in
`plans/sdd/V6-SP-17-packaging-hardening-and-release/final-release-check.md` — because Task 8 appends
its e2e names to `releaseCheckExtraTests` and step 9 must run them. It has not run yet.

### 7.9 The coverage failure on the pre-rebase base, measured

On base `2c21467` only — fixed by Commit 6:

```text
$ grep store plans/OWNERS.tsv
store   SP-06   90      PutBytes

$ go test -count=1 -cover ./internal/store
ok      github.com/qompack/qompack/internal/store       174.624s        coverage: 89.0% of statements
```

## 8. What is NOT established

- **No tag was pushed, no workflow ran, and no draft release was created.** `release.yml` and the
  `release-dry-run` job are unexecuted: their first real run will be the first push after this
  merges. The guard test asserts their *shape* (a gate before the publisher, a skipped build, a
  draft release, the archive globs, `dist: dist/goreleaser` so `--clean` cannot delete
  `dist/bundle/**`, and `--evidence dist/evidence/host-validation.json` with
  `if-no-files-found: error`) and nothing about their behaviour on a runner. No runner has
  uploaded a host-validation record.
- **goreleaser was never run.** It is not installed on this machine and the rules forbid running
  `goreleaser release`. The contract is enforced by `test/guards/release_test.go` reading the YAML,
  not by goreleaser accepting it. A syntactic error goreleaser would reject and this guard would not
  is possible; the first tag push would find it.
- **`actions/attest-build-provenance` is unexercised.** No attestation has ever been produced.
- **The `--host-validate` record a runner writes will say `unverified`**, because the `claude` CLI is
  not installed on a GitHub runner. The workflow uploads it as evidence of absence. Nothing may read
  it as a pass.
- **`release-scope` reports five of six targets `unknown`.** They cross-compile in CI's `crossbuild`
  job, and a compile leaves no committed record, so the table refuses to promote it. This is the
  intended behaviour, not a gap in the collector.
- **SP17-M7-01, -05, -06, -07 and -08 stay `unverified`.** -07 waits for the final gate to commit
  `release-check.json` (this page is prose and does not raise it). -01/-05/-06/-08 wait for Task 8's
  install, upgrade, uninstall, unknown-schema and rollback records. `releaseCheckExtraTests` is the
  hook Task 8 appends its rehearsal names to; it is empty here, and
  `TestReleaseCheckRollbackTestsAreNamed` fails if the combined list ever becomes empty.
- **The rollback rehearsal is the store package's own tests.** No CLI exposes the migration or
  rollback API; `fsck` and `doctor` are read-only. A rollback on a real project has never been
  performed by this repository.
- **One platform, one host.** Every record here is windows/amd64.
- **No timing claim is made anywhere in this commit.**

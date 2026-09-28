# Releasing Qompack

How a release is cut, what each gate proves, what the release actually claims to support, and what
it deliberately does not claim. Configuration keys are named but never described here —
`docs/config-reference.md` is generated from the schema and owns every default.

**V6 release status: blocked.** The integrated candidate has reproduced retrieval-authorization
and publication-recovery failures. The historical SP-17 scope table below is evidence for its named
artifacts; it does not certify this candidate. A successful `release-check` can include skipped
steps and tests that record known failed outcomes. Inspect those artifacts and clear the mandatory
V6 gates before tagging, publishing or broadening rollout. See `plans/sdd/V6-VERIFY/` for the
current failure records.

## 1. Procedure

1. **Bump `internal/core.Version`** in its own commit. This is a gate, not a convenience: the
   release check refuses a tag whose version does not already equal the constant, so the binary can
   never report a version no commit in this repository declared. `plugin.json`'s `version` is
   generated from the same constant (`internal/pluginmanifest`), so the same commit carries
   `go run ./tools/devtool plugin-validate --write`'s regenerated `plugin/` tree; the gate's
   `plugin-validate` step fails until it does.
2. **Fill `CHANGELOG.md`'s `[Unreleased]` section** and rename it to the version.
3. **Run the gate locally** — `go run ./tools/devtool release-check` — and fix whatever it stops on.
4. **Tag and push the tag.** `.github/workflows/release.yml` is tag-triggered on `v*`.
5. The workflow runs `release-check --tag "$GITHUB_REF_NAME"`, assembles and archives the six
   bundles (a `.zip` each), generates `dist/bundle/marketplace.json` from their `checksums.txt`
   (`devtool marketplace`), uploads the host-validation record as a workflow artifact, renders the
   supported-scope table into `dist/release-notes.md`, attests build provenance for the archives
   and the marketplace document, and hands everything to goreleaser.
6. **goreleaser creates a DRAFT release.** It builds nothing — every build entry in
   `.goreleaser.yaml` is skipped — and uploads the six zips, `checksums.txt` and `marketplace.json`.
   A person reads the scope table in the draft's notes and decides whether to publish. Nothing
   reaches users because a tag was pushed.
7. **After publishing: review the marketplace pull request.** Publishing (not drafting, and not a
   pre-release) triggers `.github/workflows/marketplace.yml`, which re-downloads the six zips from
   the published release, re-verifies each against the release's `checksums.txt`, regenerates the
   marketplace from the served bytes with the release's own generator (the tag's `devtool`, not
   `develop`'s), requires it to equal the uploaded `marketplace.json`, and opens a pull request
   onto `develop` that puts it at `.claude-plugin/marketplace.json`. Re-running the job after a
   partial failure replaces its `marketplace/<tag>` branch and reuses an open pull request. Merging
   it is what makes
   `claude plugin marketplace add AidanHT/qompack` offer the release. The repository setting "Allow
   GitHub Actions to create and approve pull requests" must be on for the workflow to open it, and
   a pull request opened with `GITHUB_TOKEN` does not start CI by itself. A pre-release is tested
   by adding its `marketplace.json` asset by URL instead (`docs/install.md` §9).

One build path produces every shipped byte: `goBuildArgs` in `tools/devtool/build.go`, used by
`build`, `build-all` and `bundle` alike, with `-trimpath -buildvcs=false -ldflags "-s -w -buildid=
-X …/internal/core.Version=<v>"`. The two extra flags are what make two builds of the same source
byte-identical, and `checksums.txt` is only meaningful because of them.

## 2. The gate

`devtool release-check [--tag vX.Y.Z] [--skip-vulncheck]` runs these in order, prints
`PASS | FAIL | SKIPPED <reason>` for each, stops at the first `FAIL`, and writes
`dist/release-check.json`.

| step | what it proves |
| --- | --- |
| version agreement | the tag, `internal/core.Version` and `git describe --tags --exact-match` all name the same version. Without `--tag` it is `SKIPPED`. |
| `ci-local` (its own eight steps, reused) | formatting, lint, vet, build, tests, coverage floors, `plugin-validate`, generated config docs |
| `build-all` | all six release targets cross-compile |
| generated docs | `docs/mcp-tools.md` and `docs/commands.md` still match their generators |
| guards | `test/guards` — the workflow pins, the co-load policy, the no-network proof, the release contract |
| govulncheck | the pinned vulnerability scan over the whole module |
| licences | `THIRD_PARTY_NOTICES.md` matches what the dependency graph actually pulls |
| real-binary determinism | the host target assembled twice, with the real compiler, into two temp directories, compared file by file |
| rollback rehearsal | the store's rollback drill before and after the first new-format write, a restored backup opening as a real store, and tamper detection. Each test name is confirmed with `-list` before it runs, because `go test -run` prints `ok` when its pattern matches nothing |
| `plugin-validate` | the committed `plugin/` tree matches what `internal/pluginmanifest` generates |
| marketplace | the marketplace generator's output for this tag passes the design validator (six `archive` entries pinned to this tag's zips, no entry `version`) and, where the `claude` CLI is on `PATH`, `claude plugin validate --strict --json`; where it is not, the step says host validation was NOT run. A committed `.claude-plugin/marketplace.json` is validated too |

`ci.yml`'s `release-dry-run` job runs the same gate (minus the tag step, plus
`--skip-vulncheck` because the `security` job already scans that commit) and then
`bundle --archive --version 0.0.0-dryrun` and `marketplace --tag v0.0.0-dryrun`, on every push. A release path first exercised on the day of a release is a
release path nobody has tested.

## 3. Supported scope

Generated by `go run ./tools/devtool release-scope --markdown` — **not written by hand**. A status
is raised only by a committed record carrying an `outcome`; prose never raises one, and no status is
ever inferred from a test passing.

<!-- release-scope: regenerate this block with `go run ./tools/devtool release-scope --markdown` -->

*Generated by `go run ./tools/devtool release-scope --markdown` on 2026-09-16 at the SP-17 final gate
(commit `4db5cea`, whose `release-check.json` is the committed record beside these tables).
Regenerate before every release; the release workflow writes the current table into the draft's notes.*

Derived by `go run ./tools/devtool release-scope` from the committed records under `plans/sdd/V6-SP-17-packaging-hardening-and-release`.
A status is raised only by a record with an `outcome` (for `release-check.json`, derived from `ok` and its step statuses); prose never raises one.

| target | status | artifact |
| --- | --- | --- |
| `linux/amd64` | unknown | — |
| `linux/arm64` | unknown | — |
| `darwin/amd64` | unknown | — |
| `darwin/arm64` | unknown | — |
| `windows/amd64` | installed-verified | commit8-install-windows-amd64/install_launcher_resolves_in_cache.json |
| `windows/arm64` | unknown | — |

`installed-verified` > `directory-validated` > `built` > `unknown`. `built` means a committed record names that target; a cross-compile proven only by CI's
`crossbuild` job leaves no record here and so reads `unknown`.

## Acceptance rows

| row | status | artifact | note |
| --- | --- | --- | --- |
| SP17-M7-01 | verified | commit8-install-windows-amd64/install_launcher_resolves_in_cache.json | — |
| SP17-M7-02 | verified | platform records | 17 record(s): 17 verified, 0 skipped, 0 failed |
| SP17-M7-03 | unverified | commit3-security-windows-amd64/posture_out_of_project_capture_is_archived.json | a security record reports failed: posture_out_of_project_capture_is_archived |
| SP17-M7-04 | unverified | commit4-fault-windows-amd64/capture_sidecar_stage_one_only.json | a fault record reports failed: capture_sidecar_stage_one_only |
| SP17-M7-05 | verified | commit8-install-windows-amd64/rollback_after_first_new_write.json | — |
| SP17-M7-06 | verified | switches + install records | — |
| SP17-M7-07 | verified | release-check.json | — |
| SP17-M7-08 | verified | release-check.json + install records | — |
| SP17-M7-07 / live-task layer | excluded | — | no live model runs are executed by this repository's tests or its shipped build, which installs no eval.LiveRunner; real-host trials run only through `devtool live-eval` (QOMPACK_LIVE_EVAL=1, agent-executed under owner decision D3), and no live run is attached here as a record |

`verified` = a record says so. `unverified` = no record says so, which is not a claim
that it is broken. `excluded` = deliberately out of scope, with the reason stated.

- **`installed-verified`** — a host actually installed the bundle and the record says so.
- **`directory-validated`** — `claude plugin validate --strict` accepted the bundle **where it
  sits**. That is the host's opinion of the manifest, not an installed-plugin canary: installed
  manifest resolution, `${CLAUDE_PLUGIN_ROOT}` expansion and launcher discovery stay unverified
  (Qompack.md §7.5).
- **`built`** — a committed record names that target.
- **`unknown`** — no committed record names it. The five non-host targets cross-compile in CI's
  `crossbuild` job, but a compile leaves no record here, and this table refuses to promote a compile
  into support.

Read that table as the release's actual claim. Everything outside `windows/amd64` is **untested at
the deployment level** and is shipped as such.

## 4. Switches

Four keys disable a **live code path**: `runtime.mode`, `runtime.migration.reinjection.sessionStartCompact`,
`runtime.daemon.enabled` and `runtime.redact.enabled`. Three of the four are measured one at a time
through the shipped bundle (`test/release`); `runtime.redact.enabled` is documented from its reader
in `internal/redact`, not measured here. `runtime.mode` gets two rows below because its two
non-default values mean different things.

| key (and value) | effect when set |
| --- | --- |
| `runtime.mode: off` | the operator's own off switch. Hooks exit 0 with an empty document before reading stdin, nothing is recorded, no daemon starts. |
| `runtime.mode: passive` | everything that **acts** is off — no reinjection, no scheduler-initiated checkpoint, no drop report — while L0/L1 keep recording and retrieval keeps answering. Passive is not a recording switch. |
| `runtime.migration.reinjection.sessionStartCompact: false` | `SessionStart source: compact` omits the injected-span open tag; the contract monitor's probe marker remains. Tool events in the same session are still recorded. |
| `runtime.daemon.enabled: false` | no resident process and no lock file. Hooks still exit 0 and still spool, which is what distinguishes a disabled daemon from a disabled plugin. |
| `runtime.redact.enabled: false` | the redaction engine is bypassed on the write path. Do not set this. |

Every other gated key is **refused, not disabled**. There is no production reader to turn off:
`runtime.migration.replacement.newResult`, `runtime.migration.experiments.enabled`,
`runtime.migration.capture.rawEvidence`, `runtime.migration.publication.durableFrontier`,
`runtime.migration.compaction.automaticVeto` and `runtime.telemetry.enabled` are rejected by
validation, restored to their defaults, and recorded in `state/config-violations.json`. Setting one
changes nothing except that file. The distinction matters: "disabled" implies something was running.

A configuration declaring a `settingsVersion` newer than this build resets the **whole**
`runtime.migration` block to defaults with a warning, rather than honouring the leaves it
recognises. Unknown future behaviour is disabled, never guessed at.

## 5. Rollback

The plan's sequence, verbatim:

> Before new-format writes, rollback can restore the verified old representation/reader. After new
> writes, verify a compatible reader or restore the backup plus supported recovery path; do not
> imply automatic downgrade. Disable output replacement first when its recovery preconditions fail;
> independent switches also control reinjection, experiments and recording. Recording can be
> disabled for privacy while permitted historical retrieval remains explicitly scoped. Uninstall
> removes integration/binaries according to a documented policy, retaining or explicitly deleting
> data by the user's choice. Do not promise secure physical erasure across backups/media.

> The rollback sequence is feature disablement, verified compatible reader/backup restoration,
> recovery check, then only separately approved reenabling.

**The legacy migration rehearsal remains a separate API.** `internal/store` carries the migration and
rollback API — `TakeBackup`, `VerifyBackup`, `RestoreBackup`, and `RehearseRollback`, which stops
writers, verifies the backup, restores into a **scratch** root (never the live project), opens it as
a real store, and re-reads every imported object through its old identity. Its drill record carries
`AutomaticDowngrade`, and that field is **always false**: a drill that cannot pass is a *result*,
not an error, and nothing in this product downgrades a format on its own.

The operator [backup and restore commands](backup.md) expose consistent backup and restoration into
a fresh destination without enabling legacy import or cutover. Stop the source writer, retain the
original store and later writes, and restore with the identified candidate. The same-build reader
proof and integrity results do not establish older-release compatibility. Activate a recovered
project only after its intended reader, project snapshot and required UAT checks pass.

`fsck --repair --yes` performs five explicit repairs and never deletes anything; see
`docs/security.md` §7.

## 6. Licences

`THIRD_PARTY_NOTICES.md` is generated by `devtool licenses --write` and checked by the release gate.
It reproduces, in full, the licence of every module that `go list -deps ./cmd/qompack` actually
reaches on at least one release target, and lists every other direct dependency as build- or
test-time only — a claim `devtool lint --only=bindeps` re-checks on all six targets rather than
asserting.

## 7. Not claimed

- **No native context control.** Qompack cannot cut the host's own history, control its compaction
  markers, or guarantee the model complies with anything it injects.
- **No exact-history guarantee.** What is recorded is what the hooks delivered. A record without an
  envelope has fidelity `unknown`, never `exact`.
- **No universal performance or storage figure.** No number on any page of these docs claims a
  savings ratio or a latency that holds on your machine.
- **No installed-host verification outside windows/amd64**, and none at all for install, upgrade or
  uninstall until the records in §3 say otherwise.
- **No secure erasure.** Deleting `.qompack/` deletes the store; it does not promise anything about
  backups, copies or snapshotting filesystems.
- **No automatic downgrade.** See §5.
- **No code signing (open release item).** The release's binaries carry no Authenticode signature.
  Windows Defender's machine-learning detection has flagged development builds of this tree as
  `Trojan:Win32/Bearfoos.A!ml` and `B!ml` and blocked or quarantined them (V6 close-out decision
  D32). An unsigned binary is more likely to be flagged, and a user can check one only against the
  release's `checksums.txt` and its build-provenance attestation. Signing the Windows binaries
  before a public release is open and unowned. What a user sees and does meanwhile is
  [troubleshooting §7](troubleshooting.md#windows-defender-flags-qompackexe).
- **No network and no telemetry**, now or by configuration — `docs/security.md` §9.
- **The release workflow is unverified in the available evidence.** The `dist: dist/goreleaser` split (so `--clean` cannot
  delete `dist/bundle/**` or `dist/release-notes.md`) and the host-validation upload
  (`--evidence dist/evidence/host-validation.json`, `if-no-files-found: error`) are YAML shape
  only. The tag-triggered draft and host-validation upload remain unverified here. Other workflows
  have run: `plans/sdd/V6-remediation/github-nightly-35704111100-jobs.json` records one nightly
  run of 29 jobs, 26 success and 3 failure (`race-windows`, `bench-deep` on both runners). That run
  was on a historical source (`9c84e31d`), not this candidate tree, so it is evidence a runner
  exercised the CI graph once — not that the current source has passed CI.

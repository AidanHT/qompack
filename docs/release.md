# Releasing Qompack

How a release is cut, what each gate proves, what the release actually claims to support, and what
it deliberately does not claim. Configuration keys are named but never described here —
`docs/config-reference.md` is generated from the schema and owns every default.

**Release status: 0.3.1, a patch release on 0.3.0** (V6 close-out decisions D81 and D82). Its
product code is 0.3.0's (the tag `v0.3.0`, `1a368a4b`) plus five fix branches, `fix/v031-spoolid`,
`fix/v031-status`, `fix/v031-flakes`, `fix/v031-ci` and `fix/v031-settle`. They fix 0.3.0's known
issues 17, 19 and 20 and the `checkpoint.decision_read_error` count (D76(d)), and change tests and
CI without loosening a check (D81(c)(5)). Its version commit is in: `internal/core.Version` and
`plugin.json` say `0.3.1` (§1, step 1). Its release gate is decision D81(c)(8): hosted `ci.yml` and
`nightly.yml` on the merged branches, the Linux `-race` run on the touched packages, the C1.16 rig
under D78's co-load condition with 0 missing Reads and 0 `LOUD.log` spool drops,
`release-check --tag v0.3.1` on the reference host on AC power, and the install from the published
marketplace on Windows and Linux. [Its release notes](release-notes/v0.3.1.md) carry the evidence
from before the tag, and on the tag push `release-check` fails while they still say it is to come
(§1, step 2). 0.3.1 changes the status of no row in the
[capability table](#capability-status-at-030); one row's residual is narrower, as the row says. The
rest of this status describes 0.3.0.

**0.3.0, released on 2026-10-08 from candidate 8.** Release 0.3.0 is cut from release candidate 8
(decision D58(e)), commit `3ec62ad2`, whose frozen bundles and evidence are recorded in
`plans/sdd/V6-closeout/phase3/c8-CANDIDATE.md`. The tag `v0.3.0` is on `1a368a4b`, a
descendant of candidate 8 whose changes reach no bundle, and every published `bin/` is
byte-identical to candidate 8's frozen bundles (V6 close-out decisions D79 and D80,
`plans/sdd/V6-closeout/phase3/c8/release-bin-compare.txt`). Its version commit is in:
`internal/core.Version`, `plugin.json` and the bundles all say `0.3.0` (§1, step 1). Nothing was
tagged or published until the candidate's verification, live and evaluation evidence was complete
and every red in it was fixed or carried a recorded disposition (V6 close-out decision D33; the
gates are Phases 3 to 7 of `plans/V6-CLOSEOUT-CHECKLIST.md`). `release.yml` run `37738581717`
built and drafted the pre-release from the tag; the install from its marketplace was rehearsed and
the release promoted (D80, [install §9](install.md#9-installing-from-the-public-marketplace)).

Candidate 8 changes product code (among them the drain pass budget, the session registry, the
checkpoint writer, the hook configuration path, the contract reading, the rehydration block and the
command client; D58(e), D60(f), D61), so earlier candidates' machine evidence does not carry to it
by a byte comparison. Its own evidence is recorded by decisions D75, D76 and D77:

- **Night chain.** `plans/sdd/V6-closeout/coordinator/overnight-c8.sh` ran 19 steps on the frozen
  tree, every one on AC power. 16 passed: the six C5.2 chunks below, the AC-gated Windows timing
  set and X11, the Windows and Linux `-race` lanes, two byte-identical bundle builds, the quiet
  C5.1 run on Windows (B-A p99 16.4 ms and B-B p99 11.3 ms against 50, B-E p99 166.8 ms against
  2,000, B-F p99 73.7 ms against 250) and the local `release-check --tag v0.3.0` on the reference
  host, all 18 of its steps `PASS` and none skipped. The chain runs that check in an isolated
  scratch clone that holds the tag, never in the shared repository (§1, step 3; D57(a), D57(d),
  D62). The other three were the Linux container's timing steps: two failed B-A and B-B alone, the
  class D53(b) does not verify in target, with 0 captures lost, and its quiet C5.1 run is reported
  only (D75(a)).
- **C5.2 and C1.16.** The C5.2 night re-measures every benchmark in full against `cf31e01`, in
  package chunks that may span more than one night and never run on battery (D62(b), D65(b),
  D65(c)). Six of its eight chunks ran complete on candidate 8's night. No row is slower than the
  base with significance except negknow's `BenchmarkOpen` on Windows (1.16x, 59.66 ms inside its 300
  ms budget, off the hot path) and symbols' `BenchmarkEnclosing_100KB` (1.011x), both recorded, not
  acted on (D75(b)). The Linux checkpoint and other chunks and the C1.16 rig re-measure, whose
  figure `docs/architecture.md` restates (D62(c), D65(a)), were measured on the C5.2 night of
  2026-10-07/08: both Linux chunks passed, with `BenchmarkFinalize` 1.32x and negknow
  `BenchmarkOpen` 1.34x on the container, each inside its budget as decision D54 recorded (D78(a));
  and the rig's compact answer under same-session ingest was the rehydration every time, at p99 143
  ms with no extra load and 417 ms under its in-process fsync and CPU co-load (D78(e)).
- **Hosted runs.** `ci.yml` run `37562946379` concluded success on its second attempt, a re-run
  of the failed jobs on the same commit (C7.2). Attempt 1's two reds are dispositioned by D75(c):
  `test (macos-latest)` failed a fixture-sanity count that is a test defect, and
  `test (windows-latest)` ended `internal/daemon` with a crash whose cause is undetermined and
  which did not recur. `nightly.yml` run `37562945914` passed. The hosted `release-dry-run`
  bundles were byte-identical to candidate 8's frozen ones, all 91 files in both directions
  (D53(h)(4), D58(e)).
- **Live re-check.** 20 real sessions on the frozen bundle passed every scenario they ran, with 650
  hook calls and no host-reported failure or timeout (D76; D53(i), D59, D60(f)).
- **Live evaluation.** C5.5 ran on the frozen bundle, and its pre-registered decision reads
  "inconclusive — interval [-0.214, 0.214] straddles -0.200". Under amendment A8 that verdict
  allows the release (D77). At this sample size it is the expected verdict, by design, and it is
  not evidence that Qompack adds nothing (A8, item 3). The recovery outcome reads "recovery
  advantage not shown". No page claims that Qompack improves recovery, task success or constraint
  retention (A8, item 2).

After the tag come the pre-release, the install rehearsal from it (D53(h)(3)), the check that the
published `bin/` bytes equal the frozen bundles, and only then the promotion. The generated SP-17
scope table in §3 is evidence for its named artifacts only; the capability table beside it states
what 0.3.0 ships and what it does not claim. A successful `release-check` can include skipped steps,
so read its record before tagging.

## 1. Procedure

1. **Bump `internal/core.Version`** in its own commit. This is a gate, not a convenience: the
   release check refuses a tag whose version does not already equal the constant, so the binary can
   never report a version no commit in this repository declared. `plugin.json`'s `version` is
   generated from the same constant (`internal/pluginmanifest`), so the same commit carries
   `go run ./tools/devtool plugin-validate --write`'s regenerated `plugin/` tree; the gate's
   `plugin-validate` step fails until it does.
2. **Fill `CHANGELOG.md`'s `[Unreleased]` section** and rename it to the version. In the same
   docs-only commit (a descendant of the candidate whose changes reach no bundle, D58(e)), rewrite
   the evidence a candidate's pages carry while its own is still owed, from the tagged candidate's
   recorded evidence (its `plans/sdd/V6-closeout/phase3/cN-CANDIDATE.md`, its night chain, hosted
   runs, live re-check, C5.5, its C5.2 nights and the C1.16 re-measure):
   `docs/release-notes/<tag>.md`'s verified-where paragraph and table, `README.md`'s
   verified-where paragraph and table, `CHANGELOG.md`'s introduction and Known limits, this page's
   release status, and the C1.16 paragraph of `docs/architecture.md` §7, which restates the rig's
   figure from the C5.2 night (D65(a)); and fill the Known issues sections of the notes and
   `CHANGELOG.md` from the close-out ledger (D66(d)). release.yml publishes the notes verbatim. On
   the tag push the `guards` step of `release-check` fails while any of those pages still carries
   one of the interim sentences it lists (`releaseInterimMarkers` in
   `test/guards/releasenotes_test.go`), such as the notes' figures standing until the candidate's
   own are recorded or the hosted runs not yet run. Until this commit writes the version's
   `CHANGELOG.md` heading, every other run requires each listed sentence to be still on its page, so
   a reworded interim sentence fails instead of slipping past the list. The guard catches only
   those sentences: rewriting the rest, the candidate 6 and 7 rows of both tables among it, stays
   this step's manual duty.
3. **Run the gate locally, on the reference host** — `go run ./tools/devtool release-check` — fix
   whatever it stops on, and keep the run's `dist/release-check.json` as the release's record of the
   fsync-bound rows. The hosted gate at step 5 reports those rows instead of gating them (§2), so
   this run is the only one that judges them at their limits before the tag, and a run on a
   GitHub-hosted runner or any other non-reference disk does not stand in for it. On the Windows
   reference host the run is made on AC power: a run on battery is not a reference measurement,
   neither a pass nor a fail (D57(d)).
4. **Tag and push the tag.** `.github/workflows/release.yml` is tag-triggered on `v*`.
5. The workflow runs `release-check --tag "$GITHUB_REF_NAME"`, assembles and archives the six
   bundles (a `.zip` each), generates `dist/bundle/marketplace.json` from their `checksums.txt`
   (`devtool marketplace`), uploads the host-validation record as a workflow artifact, renders the
   draft's notes into `dist/release-notes.md` (the hand-written `docs/release-notes/<tag>.md` when
   it exists for the tag, otherwise the generated supported-scope table), attests build provenance
   for the archives and the marketplace document, and hands everything to goreleaser.
6. **goreleaser creates a DRAFT release, marked as a pre-release** (D53(h)(2)). It builds nothing —
   every build entry in `.goreleaser.yaml` is skipped — and uploads the six zips, `checksums.txt`,
   `marketplace.json`, `LICENSE` and `THIRD_PARTY_NOTICES.md`. Every zip carries the last two at its
   root as well: the binary statically links the Go runtime and standard library, go-winio,
   klauspost/compress and golang.org/x/sys, whose licences ask for their notices in a binary
   distribution. A person reads the draft's notes and decides whether to publish it as a
   pre-release. Nothing reaches users because a tag was pushed, and a pre-release does not trigger
   the marketplace step.
7. **After publishing: review the marketplace pull request.** A full release triggers
   `.github/workflows/marketplace.yml` exactly once, whichever way it became one: published as a
   full release directly, or published as a pre-release and later promoted (edited to clear "Set as
   a pre-release"). The workflow listens to GitHub's `released` event, which fires in both cases
   and never for a draft or a pre-release, so the route this repository uses — pre-release first,
   rehearse the install from it, then promote — opens the pull request at the promotion. The job
   re-downloads the six zips from
   the published release, re-verifies each against the release's `checksums.txt`, regenerates the
   marketplace from the served bytes with the release's own generator (the tag's `devtool`, not
   `develop`'s), requires it to equal the uploaded `marketplace.json`, and opens a pull request
   onto `develop` that puts it at `.claude-plugin/marketplace.json`. Re-running the job after a
   partial failure replaces its `marketplace/<tag>` branch and reuses an open pull request. Merging
   it is what makes the documented command,
   `claude plugin marketplace add --sparse .claude-plugin -- https://github.com/AidanHT/qompack.git`
   ([install §9](install.md#9-installing-from-the-public-marketplace)), offer the release. The
   repository setting "Allow GitHub Actions to create and approve pull requests" must be on for the
   workflow to open it, and a pull request opened with `GITHUB_TOKEN` does not start CI by itself.
   A pre-release is not in the marketplace yet, and its `marketplace.json` asset cannot be added by
   URL: Claude Code treats every `github.com` URL as a git repository (install §9). Its install is
   rehearsed from a temporary branch whose `.claude-plugin/marketplace.json` is the release's own
   asset, added in the repository form with `--sparse .claude-plugin`, which Windows needs (D80(a),
   D80(b)).

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

`ci.yml`'s `release-dry-run` job first builds the six bundles at the release's own version (read
from `internal/core.Version`) and keeps them as the `release-version-bundles` workflow artifact, so
their `bin/` bytes can be compared with the frozen candidate's before a tag is pushed. It then runs
the same gate (minus the tag step, plus `--skip-vulncheck` because the `security` job already scans
that commit) and then `bundle --archive --version 0.0.0-dryrun` and
`marketplace --tag v0.0.0-dryrun`, on every push. A release path first exercised on the day of a
release is a release path nobody has tested.

Both hosted jobs that run this gate, `release-dry-run` and `release.yml`'s `release`, declare
`QOMPACK_NONREFERENCE_DISK` (ADR 0010, Addendum 2, which holds the complete list): a GitHub-hosted
disk's fsync tail is not a product figure, so on those runners three things are reported with a
note naming the declaration instead of gated — the fsync-bound wall rows (B-A, B-B, B-E's wall
row), the §12.2 spool-submode transition in the hot-path tests (X11 and
`TestIntegration_HotPathWarmWithRealResidentState`), and X10's late or deferred prompt-reply
recovery branch, which is also written to the job summary (D56(a)). B-E_cpu, the delivery ledger
(identity, 0 lost), the population census and every structural check stay gated. The declaration
is honoured only where `GITHUB_ACTIONS=true`, so the local run of §1 step 3 on the reference host
is the gate that judges those rows at their limits. Both declarations are recorded by decision
D57(a): candidate 6's hosted `release-dry-run` failed only X11, on the runner's fsync tail, after
the same package had passed in its test step, and the job lacked the declaration the other hosted
timing jobs carry.

## 3. Supported scope

The target and acceptance-row tables below are generated by
`go run ./tools/devtool release-scope --markdown` — **not written by hand**. A status is raised only
by a committed record carrying an `outcome`; prose never raises one, and no status is ever inferred
from a test passing. The [capability table](#capability-status-at-030) after them is written by
hand from the close-out ledger and says what 0.3.0 ships.

<!-- release-scope: regenerate this block with `go run ./tools/devtool release-scope --markdown` -->

*Generated by `go run ./tools/devtool release-scope --markdown` on 2026-09-16 at the SP-17 final gate
(commit `4db5cea`, whose `release-check.json` is the committed record beside these tables).
Regenerate before every release. The release workflow writes the current table into the draft's
notes only when the tag has no `docs/release-notes/<tag>.md`; 0.3.0's notes are
[docs/release-notes/v0.3.0.md](release-notes/v0.3.0.md), because these SP-17 records predate the V6
close-out.*

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

Read that table as the release's actual claim for the artifacts it names. Outside `windows/amd64`
no Claude Code session has run: linux/amd64 was installed only, from the published marketplace in a
container with no model, where `bin/qompack` kept its executable bit and ran (D80(c)), and macOS and
the arm64 targets were never installed. Those targets are shipped as such.

## Capability status at 0.3.0

Written by hand from the V6 close-out ledger (`plans/V6-CLOSEOUT-CHECKLIST.md`, owner decisions D1 to
D67 and the defaults paragraph under them) and the release candidate 8 tree. It states what the
release ships; it is not a test result. The statuses are:

- **shipped**: on in the default configuration;
- **shipped off by default**: present in the binary, off unless a documented key turns it on, or
  refused even then;
- **not shipped**: not in this build;
- **accepted residual (Dn)**: a known limit the named decision accepted for 0.3.0, documented where
  the row says;
- **not verified in target**: no evidence from the target platform or host establishes it.

| Capability | 0.3.0 status | Where it is documented |
| --- | --- | --- |
| Capture through the seven exec-form hooks, made durable before the hook is told it was taken | shipped | [architecture §1](architecture.md#1-process-model), [§4](architecture.md#4-publication-and-durability) |
| Redaction at capture | shipped | [security §2](security.md#2-redaction-what-it-covers-and-what-it-does-not) |
| Client spools and the spool submode on a slow disk (nothing lost; the transition says so) | shipped (D53(c), D55) | [troubleshooting §7](troubleshooting.md#7-daemon-problems) |
| Hot-path budget B-A derived per platform: Linux 15 ms, Windows 50 ms, macOS 40 ms | shipped (D41) | [config reference](config-reference.md#runtime) |
| Checkpoint at `PreCompact`, sealed after this session's spooled captures are replayed within a 500 ms bound | shipped (D53(c), D55) | [troubleshooting §7](troubleshooting.md#7-daemon-problems) |
| Rehydration after a compaction, at most 9,500 characters, with the rest named as overflow and pointers for the MCP tools; a compaction that dropped material and fits no section gets a loss notice naming the loss and the restore route, never silence | shipped (D5, D59(b)) | [user guide](user-guide.md#additional-context-budget-and-overflow), [cannot-do](cannot-do.md#the-host-delivers-at-most-10000-characters-of-injected-context-whole) |
| A compact `SessionStart` answers within 5 s, with a "rehydration deferred" note when the rehydration is late or cannot be built | shipped (D9, D11) | [troubleshooting §7](troubleshooting.md#7-daemon-problems) |
| The MCP retrieval tools, paged responses bounded by `runtime.mcp.maxResponseBytes`, recall ranking Qompack's own records last | shipped (D46, D49, D50) | [MCP tools](mcp-tools.md), [user guide](user-guide.md#mcp-tools) |
| The host's saved Read deny and ask rules re-checked on every archived retrieval, failing closed | shipped (D7, D55, D56(d)) | [security §1](security.md#1-trust-boundaries) |
| The rehydration block's pointers are judged against the host's saved Read rules and the project boundary: file pointers and structured tool-argument summaries are judged whole, and a path-named value holding several paths piece by piece; a free-text summary is shown only when a whitelist proves it safe (every token built from letters, marks, digits and a small safe punctuation set, no absolute or escaping path, and no rule's literal or withheld path's name where a name starts), and every one is withheld while the rules cannot be read; and section 7's drop entries are withheld or redacted. The whitelist's over-withholding, aliases and run-time names in free text, and rules outside the saved settings are recorded limits (rows below) | shipped (D50, D60(c), D61(b)(1), D63, D64) | [cannot-do §5](cannot-do.md#the-rehydration-blocks-screen-of-free-text-summaries-has-limits) |
| Six slash commands: status, recall, pin, why, dropped, eval | shipped (D36) | [commands](commands.md) |
| Operator commands: `status`, `doctor`, `fsck` (repairs only behind `--repair --yes`), `self-test`, `config print`, `backup create`, `backup verify`, `backup restore`, `admin delivery-seal` | shipped | [user guide](user-guide.md#operator-commands), [backup](backup.md) |
| Delivery-journal rollover | shipped, on by default (D2) | [troubleshooting §7](troubleshooting.md#7-daemon-problems) |
| On Windows, the daemon runs from a verified staged copy under `~/.qompack/bin/<sha256>/` | shipped (D10) | [install §6](install.md#6-uninstalling-and-what-happens-to-your-data) |
| A session whose project root is the home directory records nothing | shipped (D18) | [troubleshooting](troubleshooting.md#qompack-is-inactive-in-the-home-directory) |
| An unappliable `runtime.redact` or `runtime.mode` stops recording; any other bad key falls back and warns | shipped (D8) | [troubleshooting §6](troubleshooting.md#6-configuration-and-schema-compatibility) |
| The `runtime.migration` and `runtime.phase7` gated switches, output replacement among them | shipped off by default; a `true` is refused | [config reference](config-reference.md#gated-switches-ship-off), §4 below |
| SP-21's admission gate (explicit opt-in only) with an empty host allowlist, and `runtime.selection.submodularEnabled` | shipped off by default | README, "What ships off, and why you should leave it off" |
| Telemetry | shipped off by default; hardwired off, no network code at all | [security §9](security.md#9-network-and-telemetry) |
| The SP-15 state-aware warning detector | shipped off by default (D56(h)) | `internal/grammar` |
| Keys with no reader in this build (`runtime.logging`, `runtime.selection.loopWarningsEnabled`, `runtime.telemetry`, `selection.deltaScoring`, `selection.submodular.lazyGreedy`) | shipped with no effect (D51) | [config reference](config-reference.md#no-effect-in-this-build) |
| A manual checkpoint command | not shipped (D36) | [cannot-do §6](cannot-do.md#no-manual-checkpoint-command) |
| `qompack bench`, and an operator command that stops the daemon | not shipped | [troubleshooting §7](troubleshooting.md#7-daemon-problems) |
| Automatic downgrade of the store format | not shipped | §5 below |
| A rotation pauses capture 2.3 to 6.8 s every 65,536 deliveries; store GC halts past 65,536 carried unacknowledged leases | accepted residual (D6, D16) | [cannot-do §4](cannot-do.md#a-rotation-pauses-capture-and-the-carried-leases-have-two-hard-bounds) |
| A compaction found at session-start's borrow limit, or a daemon spawned late, can get the client's "no answer" note and be spooled and replayed | accepted residual (D29) | [cannot-do §4](cannot-do.md#a-compaction-at-the-edge-of-session-starts-budget-can-get-the-deferred-note) |
| A prompt captured out of host order (the live-versus-spool race, and hook pid reuse, which from 0.3.1 arises only from a spool file a 0.3.0 hook left) is flagged, never renumbered; a microsecond window between the staged copy's verification and its launch | accepted residual (D35(b), D38) | [cannot-do §4](cannot-do.md#the-first-captured-prompt-is-not-always-the-first-prompt-the-host-sent) |
| A `SessionEnd` that arrives while the daemon is stopping waits in the spool for the next session | accepted residual (D35(c)) | [cannot-do §4](cannot-do.md#a-sessions-end-can-wait-for-the-next-session-when-the-daemon-is-stopping) |
| Spool submode does not switch back within a session | accepted residual (D44) | [cannot-do §4](cannot-do.md#spool-submode-lasts-until-the-session-or-the-daemon-ends) |
| A retrieval page beside interleaved redacted regions can be shorter than the largest that fits | accepted residual (D48) | [cannot-do §4](cannot-do.md#a-page-near-redacted-text-can-be-shorter-than-it-could-be) |
| PutBytes (SP06-D2) and the 256 KB `OnToolUse` row (SP08-D1) miss their budgets; the cost is after the hook's ACK | accepted residual (D54, wontfix for 0.3.0) | [cannot-do §4](cannot-do.md#two-store-write-rows-miss-their-budgets-after-the-hooks-ack) |
| A `PreCompact` that arrives while a client-spool watcher pass is already running waits behind it; the seal goes ahead at the 500 ms bound and names what it left | accepted residual (D56(e)) | [troubleshooting §7](troubleshooting.md#7-daemon-problems), [cannot-do §4](cannot-do.md#a-compaction-can-wait-behind-a-spool-replay-already-running) |
| `fsck` run beside a live daemon can report an evidence-class retention root as not held while the daemon is still publishing it; stop the daemon and run it again | accepted residual (D57(b)) | [troubleshooting §9](troubleshooting.md#9-backup-rollback-and-recovery), [cannot-do §4](cannot-do.md#fsck-beside-a-running-daemon-can-report-a-retention-root-that-is-still-being-written) |
| After a daemon is killed mid-session, the daemon that takes over can reach its idle exit without writing `index/files.json`, so `fsck` exits 1 until `fsck --repair --yes` or the next session's flush writes it (nothing lost) | accepted residual (D59) | [troubleshooting §9](troubleshooting.md#9-backup-rollback-and-recovery), [cannot-do §4](cannot-do.md#after-a-daemon-takeover-fsck-can-find-the-files-view-missing) |
| With a tier-1 original over the cap, older evolution entries are not re-admitted into unused room (authority order first; `dropped()` lists them) | accepted residual (D59) | [troubleshooting §5](troubleshooting.md#5-retrieval-that-looks-wrong), [cannot-do §4](cannot-do.md#evolution-entries-are-not-re-admitted-while-the-original-request-overflows) |
| `backup create`, `backup verify` and `backup restore` refuse while a newer `settingsVersion` is in force (after a plugin downgrade); take the backup with the newer build first | accepted residual (D59) | [backup](backup.md), [troubleshooting §6](troubleshooting.md#6-configuration-and-schema-compatibility) |
| Below the smallest loss notice, nothing is injected: a rehydration budget (`runtime.rehydrate.maxTokens`) too small for even "N items dropped; call dropped()" gets no block, the drop report records the overflow, and `LOUD.log` gets one line, so it is not silent | accepted residual (D59(b), D60(c)(ii)) | [cannot-do §4](cannot-do.md#below-the-smallest-loss-notice-a-compaction-injects-nothing) |
| The free-text whitelist over-withholds: a variable, a glob, a regular expression, a `%` escape or a `name:` shape where a path may start (`localhost:3000`, `format:%h`) withholds the summary even when it names no denied file, as does a mention of a rule's literal; the root unit applies only to a plain root, so under any other root a summary that spells the root is withheld; aliases (8.3 names, links) and names built at run time are not resolved; a structured glob (a lone Glob or recall pattern) that selects a refused file the block never recorded, without spelling its literal, is judged as written (D60(c)(iv)); the store's preview collapses runs of whitespace, so a summary is judged as collapsed; the records in sections 2 to 4, your own prompts and the model's own earlier text, are outside D50 | accepted residual (D60(c)(i), D60(c)(iv), D62(f), D64(1), D64(4), D67(l)) | [cannot-do §5](cannot-do.md#the-rehydration-blocks-screen-of-free-text-summaries-has-limits) |
| On macOS, Qompack assumes the default case-insensitive volume and compares paths and the Read rules' patterns without regard to letter case; on a case-sensitive APFS volume two names that differ only in case are read as one | accepted residual (D67(m)) | [cannot-do §5](cannot-do.md#on-macos-a-case-sensitive-volume-is-treated-as-case-insensitive) |
| A quiet live session (a long reply with no tool call, a long compaction) is counted as ended until its next hook, which revives it; nothing captured is lost | accepted residual (D62) | [cannot-do §4](cannot-do.md#a-quiet-live-session-is-counted-as-ended-until-its-next-hook) |
| A command whose connect to a running daemon misses its budget can start a second daemon, which finds the running one's lock and exits; nothing is lost | accepted residual (D61(c)) | [troubleshooting §1](troubleshooting.md#qompack-status) |
| In a project with two live sessions, another session's tool use counts toward the scheduler's bound session and can close that session's segment | accepted residual (D67(b)) | [cannot-do §4](cannot-do.md#another-sessions-tool-use-can-close-the-bound-sessions-segment) |
| Windows directory sync is a no-op on the NTFS-journaling premise; a backup reported certified can revert after a power cut | accepted residual (D24, D26) | [security §8](security.md#8-known-limitations) |
| Read rules that exist only in the running session (session-only rules, CLI flags, hook policies) | accepted residual (D7) | [cannot-do §5](cannot-do.md#it-cannot-see-every-host-permission-rule) |
| On Linux and macOS, a connect to a running daemon that has stopped accepting, with its connection queue full, fails at once and reads as no daemon, so a hook, a command or `session-start` starts a second one, which finds the running one's lock and exits (nothing lost) | accepted residual (D61(c)) | [troubleshooting §1](troubleshooting.md#qompack-status) |
| Host behaviours: file re-attachment after a compaction, binary files decoded by the host, usage categories not exposed, and tool content handed to the hook whole or not at all, so non-exact fidelity is covered by tests rather than a live session (UAT-02) | accepted residual (D45, D49) | [cannot-do](cannot-do.md), [upstream issues](upstream-issues.md) |
| A delivery cut between its index record and its link is preserved and reported, not repaired; staged copies are removed by hand after uninstall; 8.3 short names and stream suffixes are outside the textual protected-path guard | accepted residual (ledger defaults) | [install §6](install.md#6-uninstalling-and-what-happens-to-your-data), [architecture §10](architecture.md#10-what-is-not-supported) |
| Linux fsync-bound timing rows (B-A, B-B) | not verified in target (D53(b)) | README, "Supported environments" |
| The Windows hot-path budgets (B-A, B-B) on battery power: the reference figures are taken on AC with the store under a Defender-excluded path, and on battery the hot path switches to spool submode with nothing lost | not verified in target (D53(c), D53(h), D57(d)) | README, "Supported environments" |
| A Claude Code session on an installed host other than windows/amd64: Linux, macOS, windows/arm64. linux/amd64 was installed from the published marketplace at the release's install rehearsal, in a container with no model session, so no Linux session has run | not verified in target (C4.11, D34(c)) | §3's generated table, [install §9](install.md#9-installing-from-the-public-marketplace) |
| The executable bit of `bin/qompack` after a marketplace install on macOS. On Linux it is kept: after the release's install rehearsal on linux/amd64, `bin/qompack` was `-rwxr-xr-x` and ran (C7.5) | not verified in target on macOS | [install §9](install.md#9-installing-from-the-public-marketplace) |
| Installing from the published GitHub marketplace on macOS, windows/arm64 and linux/arm64. On windows/amd64 and linux/amd64 the release's install was rehearsed from its own `marketplace.json` with Claude Code 2.1.293, and the documented command then installed from develop; on windows/amd64 one real session loaded 0.3.0 through the entry. The namespace under a release entry, observed on windows/amd64: `plugin:qompack:qompack`, `mcp__plugin_qompack_qompack__<tool>` and `/qompack:<name>`, from `plugin.json`'s name (D59) | not verified in target on macOS and arm64 (D53(h)) | [install §9](install.md#9-installing-from-the-public-marketplace) |
| Inventory rows no step can execute: the warm-versus-cold delta, the binary-size check, the launcher split, the frontier-toggle pair, the human half of UAT | not verified in target (D37(c)) | `plans/sdd/V6-closeout/inventory-map.md` |
| The replay evaluation's recorded-corpus tier: no recorded corpus is committed and no test reads real transcripts, so C3.8 is judged on the replay gate | not verified in target (D67(g)) | `plans/V6-CLOSEOUT-CHECKLIST.md` |
| Durability across a real power cut, and behaviour on a really full disk | not verified in target | [security §8](security.md#8-known-limitations) |

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
- **No installed-host session outside windows/amd64.** On windows/amd64 the generated table in
  §3 reads SP-17's install records; the V6 close-out's live-lane records under
  `plans/sdd/V6-closeout/live/` are agent-executed (owner decision D3) and are not inputs to that
  table. linux/amd64 was installed from the published marketplace at the release's install
  rehearsal, in a container with no model session, and kept `bin/qompack`'s executable bit (D80(c));
  no Linux session with a model has run, and macOS and the arm64 targets were never installed.
- **No secure erasure.** Deleting `.qompack/` deletes the store; it does not promise anything about
  backups, copies or snapshotting filesystems.
- **No automatic downgrade.** See §5.
- **No code signing (open release item).** The release's binaries carry no Authenticode signature.
  Windows Defender's machine-learning detection has flagged development builds of this tree as
  `Trojan:Win32/Bearfoos.A!ml` and `B!ml` and blocked or quarantined them (V6 close-out decision
  D32). An unsigned binary is more likely to be flagged, and a user can check one only against the
  release's `checksums.txt` and its build-provenance attestation. 0.3.0 was published unsigned;
  signing the Windows binaries is open and unowned. What a user sees and does meanwhile is
  [troubleshooting §7](troubleshooting.md#windows-defender-flags-qompackexe).
- **No network and no telemetry**, now or by configuration — `docs/security.md` §9.
- **The release workflow's first run was 0.3.0's.** `release.yml` run `37738581717` on the tag
  `v0.3.0` (`1a368a4b`) passed: its `release-check`, the host-validation upload
  (`--evidence dist/evidence/host-validation.json`, `if-no-files-found: error`; artifact
  `host-validation-evidence`), the release notes and goreleaser, whose `dist: dist/goreleaser`
  split kept `dist/bundle/**` and `dist/release-notes.md` through `--clean`. It drafted the
  pre-release with the six zips, `checksums.txt`, `marketplace.json`, `LICENSE` and
  `THIRD_PARTY_NOTICES.md`, and every published `bin/` is byte-identical to candidate 8's frozen
  bundles (D80, `plans/sdd/V6-closeout/phase3/c8/release-bin-compare.txt`). That run is 0.3.0's
  whole claim: each later tag, 0.3.1's among them, runs the workflow again on the runner images of
  its day (next bullet). Before the tag, the other workflows ran on candidate 8's commit: `ci.yml`
  run `37562946379`
  concluded success, two of its jobs on a re-run of the failed jobs (D75(c) dispositions the first
  attempt's two reds), `release-dry-run` passed and its release-version bundles were
  byte-identical to candidate 8's frozen ones, and the nightly run `37562945914` passed (README,
  "Supported environments"). `release-dry-run` runs the same `release-check` the release workflow
  runs. Earlier candidates'
  runs are history: on candidate 6, `ci.yml` run `36955046276` failed `release-dry-run` on X11 on
  the hosted fsync tail, after which the job declares `QOMPACK_NONREFERENCE_DISK` (D57(a)), and on
  candidate 7, `ci.yml` run `36981590450` passed `release-dry-run` (D58(a)).
- **The hosted Linux image changes on 2026-10-19.** GitHub moves the `ubuntu-latest` label to
  Ubuntu 26 from that date (the annotation on run `36981590450`;
  `actions/runner-images` issue 14748). `ci.yml`'s `release-dry-run` and `release.yml`'s `release`
  job are pinned to `ubuntu-24.04`, the image candidate 7's `release-dry-run` ran on (job
  `110757119491`, image version 20260927.320.1), and `test/guards`'
  `TestReleaseJobsPinTheirRunnerImage` keeps them there (D62(h)): the tag-time gate and the release
  build run on the image their hosted evidence came from whenever the tag is pushed. `ci.yml`'s
  other Linux jobs, `nightly.yml`'s and `marketplace.yml`'s `pin` job stay on `ubuntu-latest`, and
  no run of this repository has been green on Ubuntu 26, so a red that appears only there after that
  date needs its own recorded disposition before the tag, like any other red (D33). The `pin` job is
  on the release path but is not pinned: it runs only after a full release exists (step 7), changes
  no release asset, re-verifies the served zips, compares bytes and opens a pull request, and a red
  there is re-run or recorded, not a rebuilt release. A full release promoted after 2026-10-19
  therefore runs that job on Ubuntu 26, the first run of it on that image. Moving the release jobs
  off `ubuntu-24.04` takes a green hosted run on the new image first.

# Releasing Qompack

How a release is cut, what each gate proves, what the release actually claims to support, and what
it deliberately does not claim. Configuration keys are named but never described here —
`docs/config-reference.md` is generated from the schema and owns every default.

**Release status: 0.3.0 candidate, not released.** Release 0.3.0 is being cut from release candidate
8 (decision D58(e)), whose commit and frozen bundles are recorded in
`plans/sdd/V6-closeout/phase3/c8-CANDIDATE.md` when it is frozen. The release tags candidate 8 or a
descendant whose changes reach no bundle, and the published `bin/` must equal candidate 8's frozen
bundles. Its version commit is in: `internal/core.Version`, `plugin.json` and the bundles all say
`0.3.0` (§1, step 1). Nothing is tagged or published until the candidate's verification, live and
evaluation evidence is complete and every red in it is fixed or carries a recorded disposition (V6
close-out decision D33; the gates are Phases 3 to 7 of `plans/V6-CLOSEOUT-CHECKLIST.md`).

Candidate 8 changes product code (the drain pass budget, the session registry, the checkpoint
writer, the hook configuration path, the contract reading, the rehydration block and the command
client; D58(e), D60(f), D61), so earlier candidates' machine evidence does not carry to it by a byte
comparison. Already recorded for candidate 7: hosted `ci.yml` run `36981590450`, green except
`test (windows-latest)`, a wall-clock margin in a spool-watcher test that now runs on an injected
clock (D58(a), D58(b)); hosted `nightly.yml` run `36981711009`, green; the hosted release-version
bundles byte-identical to candidate 7's frozen ones (D58(a)); and candidate 7's live lane, 20 real
sessions with 474 hook calls and no hook failure or timeout (D59). Still owed before the tag, all on
candidate 8: its night chain on the frozen tree
(`plans/sdd/V6-closeout/coordinator/c8-night.sh`: the AC-gated Windows timing and X11, the Windows
and Linux `-race` lanes, two reproducible bundle builds and the quiet C5.1 run); hosted `ci.yml` and `nightly.yml` (C7.2), including the hosted `release-dry-run`
bundles compared byte for byte with candidate 8's frozen ones (D53(h)(4), D58(e)); its short live
re-check (D59, D60(f)); the pre-registered live evaluation, C5.5, on its frozen bundles, whose
verdict decides the release under amendment A8 (D58(e)); and the local `release-check --tag` on the
reference host, on AC power, which the night chain runs against a local tag it deletes afterwards
(§1, step 3; D57(a), D57(d)). After the tag come the pre-release, the install rehearsal from it
(D53(h)(3)), the check that the published `bin/` bytes equal the frozen bundles, and only then the
promotion. The generated SP-17 scope table in §3 is evidence for its named artifacts only; the
capability table beside it states what 0.3.0 ships and what it does not claim. A successful
`release-check` can include skipped steps, so read its record before tagging.

## 1. Procedure

1. **Bump `internal/core.Version`** in its own commit. This is a gate, not a convenience: the
   release check refuses a tag whose version does not already equal the constant, so the binary can
   never report a version no commit in this repository declared. `plugin.json`'s `version` is
   generated from the same constant (`internal/pluginmanifest`), so the same commit carries
   `go run ./tools/devtool plugin-validate --write`'s regenerated `plugin/` tree; the gate's
   `plugin-validate` step fails until it does.
2. **Fill `CHANGELOG.md`'s `[Unreleased]` section** and rename it to the version.
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

Read that table as the release's actual claim. Everything outside `windows/amd64` is **untested at
the deployment level** and is shipped as such.

## Capability status at 0.3.0

Written by hand from the V6 close-out ledger (`plans/V6-CLOSEOUT-CHECKLIST.md`, owner decisions D1 to
D61 and the defaults paragraph under them) and the release candidate 8 tree. It states what the
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
| The rehydration block's pointers never show a path the host denies or an absolute path outside the project: file pointers and structured tool-argument summaries are judged whole, free-text summaries are screened, and section 7's drop entries are withheld or redacted | shipped (D50, D60(c), D61(b)) | [cannot-do §5](cannot-do.md#the-rehydration-blocks-screen-of-free-text-summaries-has-limits) |
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
| A prompt captured out of host order (the live-versus-spool race, hook pid reuse) is flagged, never renumbered; a microsecond window between the staged copy's verification and its launch | accepted residual (D35(b), D38) | [cannot-do §4](cannot-do.md#the-first-captured-prompt-is-not-always-the-first-prompt-the-host-sent) |
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
| The free-text screen of tool summaries does not resolve aliases (8.3 names, links) or globs, cannot see names built at run time, and withholds free text that only mentions a rule's literal; the records in sections 2 to 4, your own prompts and the model's own earlier text, are outside D50 | accepted residual (D60(c)(i), D61(b)) | [cannot-do §5](cannot-do.md#the-rehydration-blocks-screen-of-free-text-summaries-has-limits) |
| Windows directory sync is a no-op on the NTFS-journaling premise; a backup reported certified can revert after a power cut | accepted residual (D24, D26) | [security §8](security.md#8-known-limitations) |
| Read rules that exist only in the running session (session-only rules, CLI flags, hook policies) | accepted residual (D7) | [cannot-do §5](cannot-do.md#it-cannot-see-every-host-permission-rule) |
| Host behaviours: file re-attachment after a compaction, binary files decoded by the host, usage categories not exposed, and tool content handed to the hook whole or not at all, so non-exact fidelity is covered by tests rather than a live session (UAT-02) | accepted residual (D45, D49) | [cannot-do](cannot-do.md), [upstream issues](upstream-issues.md) |
| A delivery cut between its index record and its link is preserved and reported, not repaired; staged copies are removed by hand after uninstall; 8.3 short names and stream suffixes are outside the textual protected-path guard | accepted residual (ledger defaults) | [install §6](install.md#6-uninstalling-and-what-happens-to-your-data), [architecture §10](architecture.md#10-what-is-not-supported) |
| Linux fsync-bound timing rows (B-A, B-B) | not verified in target (D53(b)) | README, "Supported environments" |
| The Windows hot-path budgets (B-A, B-B) on battery power: the reference figures are taken on AC with the store under a Defender-excluded path, and on battery the hot path switches to spool submode with nothing lost | not verified in target (D53(c), D53(h), D57(d)) | README, "Supported environments" |
| An installed Claude Code host on any target other than windows/amd64: Linux, macOS, windows/arm64 | not verified in target (C4.11, D34(c)) | §3's generated table |
| The executable bit of `bin/qompack` after a marketplace install on Linux and macOS | not verified in target (C7.5) | [install §9](install.md#9-installing-from-the-public-marketplace) |
| Installing from the published GitHub marketplace. The namespace under a release entry was observed on windows/amd64 through a local marketplace entry named `qompack-windows-amd64`: `plugin:qompack:qompack`, `mcp__plugin_qompack_qompack__<tool>` and `/qompack:<name>`, from `plugin.json`'s name (D59) | not verified in target (D53(h)) | [install §9](install.md#9-installing-from-the-public-marketplace) |
| The tag-triggered release workflow | not verified in target | §7 below |
| Inventory rows no step can execute: the warm-versus-cold delta, the binary-size check, the launcher split, the frontier-toggle pair, the human half of UAT | not verified in target (D37(c)) | `plans/sdd/V6-closeout/inventory-map.md` |
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
- **No installed-host verification outside windows/amd64.** On windows/amd64 the generated table in
  §3 reads SP-17's install records; the V6 close-out's live-lane records under
  `plans/sdd/V6-closeout/live/` are agent-executed (owner decision D3) and are not inputs to that
  table.
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
  only. The tag-triggered draft and host-validation upload remain unverified here: `release.yml` has
  never run. The other workflows have run on candidate 6 and on candidate 7. On candidate 6,
  `ci.yml` run `36955046276` passed every job but `release-dry-run` and `test (windows-latest)`,
  whose failures are dispositioned by D57(a) (X11 on the hosted fsync tail; the job now declares
  `QOMPACK_NONREFERENCE_DISK`) and D57(b) (a read-order race in `test/fault`'s audit, fixed in the
  test), and the nightly run `36955043924` passed all 31 of its jobs. On candidate 7, `ci.yml` run
  `36981590450` passed every job but `test (windows-latest)`, a wall-clock margin in a
  spool-watcher test, which now runs its retry horizon on an injected clock (D58(a), D58(b));
  `release-dry-run` passed, and its release-version bundles were byte-identical to candidate 7's
  frozen ones. The nightly run `36981711009` passed all 31 of its jobs (README, "Supported
  environments"). Neither workflow has run on candidate 8 yet, and candidate 8 changes product code,
  so its own runs are owed before the tag. `release-dry-run` runs the same `release-check` the
  release workflow runs, so its result on the release commit is the nearest evidence for this path.
- **The hosted Linux image changes on 2026-10-19.** GitHub moves the `ubuntu-latest` label to
  Ubuntu 26 from that date (the annotation on run `36981590450`;
  `actions/runner-images` issue 14748). `ci.yml`'s Linux jobs, `release-dry-run` among them, and
  `release.yml`'s `release` job run on `ubuntu-latest`, and no run of this repository has been green
  on Ubuntu 26. A tag pushed, or a `release-dry-run` re-run, after that date runs on an image with
  no green run behind it: either tag before then, or first get one green hosted run on the new image
  (or pin those jobs to `ubuntu-24.04`). A red that appears only on the new image needs its own
  recorded disposition before the tag, like any other red (D33).

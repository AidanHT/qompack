# User acceptance testing

Twelve scenarios, written so that a human can run them against an installed build and leave a
record another human can audit. Each one states what must be true before it starts, the commands
and host actions it performs, the exact fields to read, the evidence to keep, and what a failure or
a skip means.

**All twelve scenarios have been executed, by an agent, not by a human**, under owner decision D3:
agent-executed on the owner's real host, in real Claude Code 2.1.280 sessions on Windows 11,
against the packaged `0.3.0` bundle that SP-17's `go run ./tools/devtool bundle` assembles (see
[docs/install.md](install.md)). All twelve first ran on 2026-09-29 against release candidate 3
(commit `d5598eb4`), six passing and six failing, with the findings routed by decision D45; under
decision D47 the eleven other rows were re-run on candidate 4 (commit `9f6a2fad`), seven passing
and four failing (UAT-03, UAT-05, UAT-06, UAT-09; UAT-05 fails because decision D50 reads its
authority-order expectation literally), with the findings routed by decisions D49 and D50. Those
eleven Result blocks report candidate 4 and keep candidate 3's outcome as a history line. UAT-10
was not re-run: its Result block still reports candidate 3, and it is re-run on the next candidate
(D50). No human has run these scenarios, automated package and installation tests have separate
evidence and do not fill these blocks, and no release has been published.

What the commands, slash commands and MCP tools *are* is [docs/user-guide.md](user-guide.md); what
each observation does and does not license you to conclude is
[docs/troubleshooting.md](troubleshooting.md) and [docs/cannot-do.md](cannot-do.md). This page does
not restate them; it links them, and a scenario's expected result is written in their vocabulary.

## The isolation rule

Run every scenario in a **disposable project** — a directory created for the run, containing
throwaway files, opened in a Claude Code session started for the run and discarded after it.

- **Never run a scenario against an active user session.** Several steps compact a session, resume
  it, fork it, or edit files while it is running. A destructive probe belongs nowhere near work
  someone cares about.
- **Some diagnostics write.** `qompack status`, `qompack self-test` and hook entry points can
  create `<project>/.qompack/` in the project directory they resolve and start that project's daemon
  ([docs/troubleshooting.md](troubleshooting.md#1-start-with-provenance)). Running one in a fresh
  directory can write there. `qompack doctor` and default `qompack fsck` are read-only.
- **One project per scenario.** Scenarios 08, 09 and 11 deliberately leave records behind; sharing a
  project between them makes a later row's `absent` or `active` unreadable.
- **Terminate only the daemon you started.** Daemons are per project and the `pid` is in
  `.qompack/run/daemon.lock`; there is no operator stop command in this build, and waiting for the
  idle exit (`runtime.daemon.idleExitSeconds`) is the preferred ending
  ([docs/troubleshooting.md](troubleshooting.md#7-daemon-problems)).

## What a skip means

A skipped scenario is not a passing one. **A skipped integration leaves the corresponding capability
unverified**, and the record has to say which capability that is: the row is the only place a reader
learns that nobody has seen the thing work. The same is true of a step that could not be reached —
record the step number, the reason, and stop, rather than substituting a step that was reachable.

A step marked `[requires SP-17 artifact]` needs an assembled, installable bundle and a host to
install, upgrade or uninstall it against. SP-17 shipped the tooling that produces the bundle and
documented those procedures in [docs/install.md](install.md). Each human run must identify its own
tested bundle and isolation directory; an automated installation record does not complete these
steps. A missing artifact or a failed prerequisite leaves the row unverified.

## The record every row carries

| Field | What goes in it |
|---|---|
| `Result` | `pass`, `fail`, `skipped — <reason>`, or `not executed — capability unverified` |
| `Snapshot` | the build: `qompack version`, the bundle's git SHA, host OS, Claude Code version |
| `Date` | the date of the run, in the operator's own time zone, stated |
| `Executed by` | who ran it |
| `Evidence` | where the captured output lives (free text; see each row's evidence block) |
| `Rollback verified` | verified backup/frontier identity, compatible reader, restored-content checks and treatment of later writes; otherwise `unverified` with the missing step |

A `fail` keeps its evidence exactly as a `pass` does. A failed scenario that was cleaned up before
anyone looked is a scenario that has to be run again.

**Recovery acceptance requires the actual operator artifacts.** Use the supported
[backup and restore procedure](backup.md). Keep the backup identity, candidate hash, same-build
reader proof, integrity report and treatment of later writes with the scenario. These commands do
not establish old-release compatibility or perform automatic downgrade. A missing compatibility,
frontier or activation check remains unverified; a clean integrity report cannot substitute for it.

## How to run a scenario

1. **Record the snapshot before anything else.** `qompack version` prints the plugin version and
   nothing else — on this tree, built from source, it printed `0.1.0` and exited `0` (observed in an
   empty scratch directory outside the repository; it created no `.qompack/` there). Record beside
   it the git SHA the bundle was built from, the host OS and its version, and the Claude Code
   version, so the row names one build and not a family of them.
2. **Take and verify a pre-run backup.** With the disposable project's daemon stopped, run
   `qompack backup create --project <root> --id <unique-before-id> --json`, then
   `qompack backup verify --project <root> --id <unique-before-id> --json`. Save both outputs.
   If no store exists yet, record that initial absence. Preserve the project-file snapshot too;
   a store backup does not back up the user's working files.
3. **Run the steps in order**, capturing each command's stdout, stderr and exit status.
4. **Read the expected-result block field by field**, not impressionistically. Where the block says a
   string is "to be confirmed at execution", the first run's job is to record what the string
   actually was — not to decide whether it was close enough.
5. **Stop the writers, restore, and check.** Confirm the daemon belongs to this disposable
   project before stopping it; a lock-file PID alone is insufficient. Run
   `qompack backup restore --project <root> --id <unique-before-id> --destination <fresh-recovery-root> --json`.
   Save the same-build proof and integrity results. Keep the original post-run store and later
   writes intact. Test the intended reader against the recovered project and matching project-file
   snapshot before any activation. Record missing cross-version, frontier or activation checks as
   unverified, even if the same-build restore succeeds.
6. **Fill the Result block in this file** (or in the run's own copy of it) and file the evidence.

Do not repair a disagreement by editing data: `checkpoints/`, `pins/` and `sketches/tried.bloom` are
append-only by construction and nothing under `.qompack/` may be trimmed to make a step look right
([docs/architecture.md §2](architecture.md#2-write-set-and-retention)).

---

## UAT-01

**Scenario**

Install the plugin, then record what is actually installed: the bundle and its version, the host and
its version, the operating system, the hooks and tools the host discovered. Every optimization this
build does not support is disabled, and can be seen to be disabled.

**Preconditions**

- The packaged bundle `[requires SP-17 artifact]`, and a disposable project.
- A `qompack` binary on `PATH`, or the bundle's own `bin/qompack`.
- No `<project>/.qompack/` yet; if one exists, take and verify the pre-run backup first.
- Configuration: defaults only. No project or user `config.json`, no `QOMPACK_*` variables, no
  `--set` overrides — so every leaf reads `default` and the run is about the shipped build.

**Steps**

1. Install the plugin into Claude Code from the packaged bundle. `[requires SP-17 artifact]`
2. `qompack version`, and record the output verbatim.
3. Record the bundle's git SHA, the host OS and its version, and the Claude Code version.
4. Record the declared plugin version from `.claude-plugin/plugin.json` at the installed bundle's
   root.
5. Record the hook entry points the host registered, from `hooks/hooks.json` at the installed
   bundle's root, and compare them with what the host reports it loaded. `[requires SP-17 artifact]`
6. Record the MCP tool inventory the host lists for the `qompack` server, and compare it with
   [docs/mcp-tools.md](mcp-tools.md).
7. `qompack config print --provenance`, captured to a file.
8. `qompack self-test`, captured to a file.

**Expected observable result**

- Step 2 prints the plugin version on a line of its own and exits `0` — `0.1.0` on this tree
  (observed; the value is `core.Version`, so a later build prints its own).
- Step 4's `version` field is the version the bundle was stamped with: the same value step 2
  printed and the `version` in the bundle's `BUNDLE.json`, because `devtool bundle` stamps one
  version into the binary, `plugin.json` and `BUNDLE.json` alike, and strips the source tree's
  `plugin/` prefix so the manifest sits at the bundle's root. The source tree's own
  `plugin/.claude-plugin/plugin.json` reads `0.1.0` until the release's version commit, and the
  repository's last git tag does not agree with it either; record all three as they stand
  ([README.md](../README.md#status-pre-release)).
- Step 5's hook list is the seven events `plugin/hooks/hooks.json` declares — `PostToolUse`,
  `PreCompact`, `SessionEnd`, `SessionStart`, `Stop`, `SubagentStop`, `UserPromptSubmit` — each
  invoking `${CLAUDE_PLUGIN_ROOT}/bin/qompack` with its own subcommand and timeout (read from that
  file). What the host reports having loaded is the half that has never been observed: installed
  manifest resolution and `${CLAUDE_PLUGIN_ROOT}` expansion are `implemented_unverified`
  ([README.md](../README.md#supported-environments)). Record what it says; do not assume a match.
- Step 6's inventory is the eight tools [docs/mcp-tools.md](mcp-tools.md#tools-at-a-glance) lists:
  `recall`, `expand`, `re_read`, `already_tried`, `record_eliminated`, `timeline`, `why`, `dropped`.
  An inventory that differs is a finding, not a documentation error to be papered over.
- Step 7: every gated switch reads `false`. They are printed as JSONC leaves inside their blocks,
  not as dotted lines — observed on this tree, the form is `"newResult": false  // default
  config.Defaults()`. The ten leaves are `runtime.migration.capture.rawEvidence`,
  `runtime.migration.publication.durableFrontier`, `runtime.migration.replacement.newResult`,
  `runtime.migration.compaction.automaticVeto`, `runtime.migration.experiments.enabled`,
  `runtime.phase7.reuse.scopedCandidates`, `runtime.phase7.reuse.warmPrior`,
  `runtime.phase7.retrieval.reminders`, `runtime.phase7.retrieval.demandPromotion` and
  `runtime.phase7.filters.segmentBloom`
  ([Gated switches](config-reference.md#gated-switches-ship-off)). `runtime.telemetry.enabled` and
  `runtime.migration.compaction.blockManualCompact` are `false` as well and are hardwired so.
- Step 8 prints a table with `OK`, `SEVERITY` and `OBSERVED` columns. **Read `OBSERVED`.** A row
  whose observation is `not-yet-implemented`, `first-session`, `no-transcript-path`, `unset` or any
  other spelling in `internal/contract/observation.go`'s no-observation table made no assertion at
  all ([docs/troubleshooting.md](troubleshooting.md#1-start-with-provenance)). In a zero-Services
  run on this tree, `hook.additional_context_delivered`, `precompact.has_time_to_write`,
  `precompact.custom_instructions_accepted` and `mcp.server_registered` all read
  `not-yet-implemented`; against an installed host their values are what this scenario exists to
  find out, and are **to be confirmed at execution** — except
  `precompact.custom_instructions_accepted`, which reads `retired` wherever its producer is
  declared: no host accepts a PreCompact instruction, so Qompack emits none (C1.18), and an `ok`
  there is no evidence of anything.

**Evidence to record**

`uat/UAT-01/` outside the repository: `version.txt`, `config-provenance.txt`, `self-test.txt`,
`self-test.json` (`qompack self-test --json`), the host's own plugin/tool listings as text or
screenshots, and a `snapshot.txt` naming the four snapshot values.

**Failure and rollback outcome**

A **fail** is: a gated switch reading anything but `false`; a tool or hook inventory that differs
from the two files above with no explanation; a `self-test` row reporting a contract *broken*
(severity `critical`, `OK` false) rather than unobserved. A `self-test` row that observes nothing is
**not** a fail — it is the recorded gap §1 of the troubleshooting page describes, and the row says
so. A **skip** is: no installable artifact, so steps 1, 5 and the host halves of 6 cannot run; the
capability left unverified is installed-host discovery.

Rollback: stop the verified disposable-project writer and follow the shared backup/restore
procedure above. Preserve the post-run store and later writes; restore the named backup into a
fresh recovery project and validate its matching project-file/configuration snapshot. When the
initial state was absent, retain this run as evidence and use a fresh empty project. Record the
reader proof, integrity results and any missing cross-version or activation checks. Do not edit
stored records to reconcile a failure.

**Result**

```text
Result: pass — frozen bundle installed through a disposable local marketplace at local scope
  (real profile, no QOMPACK_* variables, no config file); the session's init listed
  plugin:qompack:qompack connected with the eight documented tools and the six /qompack:
  commands (no qompack:checkpoint); SessionStart, UserPromptSubmit, PostToolUse and Stop fired
  (PreCompact, SubagentStop not exercised by a two-turn session; SessionEnd ran per the store but
  has no host stream event); step 2 printed 0.3.0, exit 0; step 4's installed plugin.json reads
  0.3.0 = step 2 = BUNDLE.json (source tree plugin/.claude-plugin/plugin.json reads 0.1.0, last
  tag v0.2.0); all 108 leaves default, every gated switch false; self-test exit 0, no critical
  row. Step 8 OBSERVED (to be confirmed at execution): hook.additional_context_delivered,
  precompact.has_time_to_write, precompact.custom_instructions_accepted and
  mcp.server_registered read not-yet-implemented in the installed CLI self-test; the daemon's
  own snapshot, where the producers are declared, read not-yet-observed, timeout-unknown,
  retired and initialize-pending. Candidate 3's findings re-checked after the MCP call: `qompack
  fsck --json` exit 0 with the daemon live and stopped, and with --seal-check (the MCP record is
  indexed at its call's turn 3); `qompack status` "host contract: 9 assertion(s), all holding";
  `qompack doctor --json` exit 0, captures.unpublished "0 gap(s) across 7 sidecar(s)", agreeing
  with fsck.
  Candidate 3 (d5598eb4): pass — with findings: fsck exit 1 (MCP record at turn 0), status
  "2 of 9 FAILING", doctor vs fsck disagreeing, evidence
  plans/sdd/V6-closeout/live/uat/UAT-01/
Snapshot: qompack version 0.3.0; bundle BUNDLE.json sha256
  aa7da0e17b7597562a6eba47fc48f1db81ff5e9bdc494b9997a323137625558d; commit
  9f6a2fadf086eba8080af589a35dd9554ae6cab4; Windows 11 Home 25H2 build 10.0.26200.9457;
  Claude Code 2.1.280
Date: 2026-09-29 (America/Toronto)
Executed by: Claude Code workflow subagent (Opus 5.5), agent-executed on the owner's real host
  per owner decision D3 — not human UAT
Evidence: plans/sdd/V6-closeout/live/rerun-c4/UAT-01/ (notes.txt indexes it)
Rollback verified: not applicable — initial state absent (no <project>/.qompack/, recorded);
  per the row's rule the run is retained as evidence and no pre-run store exists to restore;
  no backup or restore was run in this row
```

---

## UAT-02

**Scenario**

Start a session with the plugin installed and do ordinary work in it. What Qompack was permitted to
record is visible, each captured result says how faithful it is, and a capture that was cut,
redacted or opaque says so rather than appearing as nothing.

**Preconditions**

- UAT-01 complete, or its steps folded in. `[requires SP-17 artifact]`
- A fresh disposable project with a few small text files and one large file (comfortably over
  `runtime.mcp.maxResponseBytes`, default `262144`) and one binary file.
- Configuration: defaults. `runtime.redact.enabled` stays `true`, `runtime.mode` stays `auto`.
- Verified pre-run backup of `.qompack/` (or recorded initial absence) (expected: does not exist).

**Steps**

1. `qompack self-test` in the project, captured. This creates `.qompack/` and starts the daemon.
2. Start a Claude Code session in the project. `[requires SP-17 artifact]`
3. Submit one prompt that reads one of the small files.
4. Run a tool call whose output is large — reading the oversized file — and one whose output is the
   binary file.
5. `qompack status`, captured.
6. In the session, `/qompack:recall <a phrase from the small file>` and, for a hit, call the
   `expand` tool on its hash.

**Expected observable result**

- Step 1's table is the eight self-checks (`config.load`, `config.capture`, `qompack.writable`,
  `paths.guard`, `ipc.resolve`, `daemon.reachable`, `admin.ping`, `ops.coverage`) followed by the nine
  host-contract
  assertions ([docs/troubleshooting.md](troubleshooting.md#1-start-with-provenance)). In a real
  session's project, the assertions that read `first-session` or `not-yet-implemented` in an empty
  directory may read something else; what they read here is **to be confirmed at execution**.
- Step 5's first line is the provenance of everything under it: `source: daemon (available, …)`,
  `disk`, or `none` ([docs/user-guide.md](user-guide.md#qompackstatus)). Per-hook latency rows that
  have no instrument read `unavailable` **with a reason**, never `0`
  ([docs/troubleshooting.md](troubleshooting.md#2-unknown-capability-or-telemetry)).
- Step 6's `expand` response carries `_meta.qompack` fields and reports the read itself — `span`,
  `total_bytes`, `truncated` and, when there is more, `next_span` — with anything today's privacy
  policy removes shown as a `«redacted:…»` placeholder. It carries no fidelity or coverage field:
  the small file's capture fidelity, which should be `exact` — *the captured host delivery*, not the
  completeness of the file — is recorded on its capture sidecar under `.qompack/records/captures/`,
  which no tool surfaces ([docs/user-guide.md](user-guide.md#fidelity-coverage-and-error-states),
  [docs/mcp-tools.md](mcp-tools.md#expand)).
- Step 6 expectation revised under D46 (2026-09-29).
- The oversized and binary captures are the point of the row: each must come back with a
  **non-`exact`** fidelity — one of `prefix`, `partial`, `truncated`, `redacted`, `binary`,
  `failure` or `unknown` — and must come back, rather than being silently absent. Which value each
  one takes on a real host is **to be confirmed at execution**; the enumeration is
  `internal/core/evidence.go`'s and is fixed.
- No step produces a claim that native context shrank; nothing in Qompack's output does
  ([docs/cannot-do.md](cannot-do.md#2-proof)).

**Evidence to record**

`uat/UAT-02/`: `self-test.txt`, `status.txt`, the session transcript export if the host offers one,
the `recall` and `expand` responses as JSON, and a copy of `.qompack/logs/qompack-YYYYMMDD.log` and
`.qompack/index/*.jsonl` taken at the end of the run.

**Failure and rollback outcome**

A **fail** is: an oversized or binary capture that is absent from the archive with no fidelity
recorded anywhere; a fidelity of `exact` on a capture that was demonstrably cut; a latency cell
printing `0` where no instrument exists. A **skip** is: no host session, leaving permitted-capture
identity and fidelity unverified.

Rollback: stop the verified disposable-project writer and follow the shared backup/restore
procedure above. Preserve the post-run store and later writes; restore the named backup into a
fresh recovery project and validate its matching project-file/configuration snapshot. When the
initial state was absent, retain this run as evidence and use a fresh empty project. Record the
reader proof, integrity results and any missing cross-version or activation checks. Do not edit
stored records to reconcile a failure.

**Result**

```text
Result: pass on fail criteria; row capability host-limited (D49) — no fail criterion occurred
  (every capture came back; no demonstrably
  cut capture reads exact; latency cells without an instrument read `unavailable` with a reason,
  never 0). Step 6 as revised under D46 holds: the expand responses carry `_meta.qompack` (span,
  total_bytes, truncated, and next_span "9909:16384" for the paged big.log) and the redacted
  creds.txt content as «redacted:aws_access_key_id» / «redacted:github_token», and no retrieval
  response carries a fidelity or coverage field; the small file's capture sidecar reads `exact`.
  Expected-result field NOT observed (finding, unchanged since candidate 3): the oversized and
  binary captures read `exact` (sidecars 35 exact, 1 redacted) — the host delivered big.log
  (310,800 B) whole and Qompack stored the whole delivery; Read refuses blob.bin and fires no
  PostToolUse, and `cat blob.bin` arrives as host-decoded text — so the non-exact capability the
  row exists to show is unverified on this host. C4.2 re-checked after a session with six MCP calls:
  all seven hook events fired and the host counts match the store (prompts 10 = 10; tool uses 15
  minus the refused Read = 14 = 14 captures; sessions 1 = 1; no MCP record out of turn order);
  `qompack status` "9 assertion(s), all holding", `doctor --json` with no degraded row and
  `fsck --json` exit 0 with every row ok, with the daemon live and again after the default idle
  exit. Planted secrets: 0 hits in the post-run store (raw, decompressed and decoded). Step 1
  OBSERVED (to be confirmed at execution, before any session): session_start.fires first-session,
  session_start.source_compact no-precompact-pending, hook.additional_context_delivered /
  precompact.has_time_to_write / precompact.custom_instructions_accepted / mcp.server_registered
  not-yet-implemented, hook.payload_shape "payload shape valid", transcript.readable
  no-transcript-path, plugin.root_resolves unset.
  Candidate 3 (d5598eb4): pass — with findings: fsck exit 1 (turn-0 MCP records, a segment naming
  an unwritten checkpoint), doctor over-counting capture gaps, evidence
  plans/sdd/V6-closeout/live/uat/UAT-02/
Snapshot: qompack version 0.3.0; bundle BUNDLE.json sha256
  aa7da0e17b7597562a6eba47fc48f1db81ff5e9bdc494b9997a323137625558d; commit
  9f6a2fadf086eba8080af589a35dd9554ae6cab4; Windows 11 Home 25H2 build 10.0.26200.9457;
  Claude Code 2.1.280
Date: 2026-09-29 (America/Toronto)
Executed by: Claude Code workflow subagent (Opus 5.5), agent-executed on the owner's real host
  per owner decision D3 — not human UAT
Evidence: plans/sdd/V6-closeout/live/rerun-c4/UAT-02/ (notes.txt indexes it; also C4.2)
Rollback verified: not applicable — initial state absent (recorded: `backup create` exit 1 "no
  existing store"); per the row's rule the run is retained as evidence; no restore was run
```

Note: non-exact fidelity is host-limited on Claude Code 2.1.280; covered by
`TestCapturePolicyProducesTheCompleteFidelitySet`, `TestHookCapture_OversizeRetainsClassificationWithoutOpaqueBytes`,
`TestHookCapture_BinaryPayloadIsClassifiedNotSilentlyRejected`, `TestHookCapture_ShortReadIsRecordedAsPartial`
and `TestCaptureSidecar_DegradedCaptureIsRecordedAsSuch` (D49, 2026-09-30).

---

## UAT-03

**Scenario**

A checkpoint is written when the host compacts, and what it wrote survives the daemon going away and
coming back — or the incompleteness is stated outright rather than being papered over with an older
checkpoint presented as the current one.

**Preconditions**

- A session that has done enough work to compact. `[requires SP-17 artifact]`
- Configuration: defaults. `checkpoint.budgetTokens` at `12000`,
  `checkpoint.frontier.advanceOnSegmentClose` at `true`, `runtime.daemon.enabled` at `true`.
- `runtime.migration.publication.durableFrontier` is `false` and stays `false` — durable publication
  of references and the committed frontier is a gated capability whose gate has not passed
  ([Gated switches](config-reference.md#gated-switches-ship-off)). This scenario therefore tests
  what the shipped build does, and records the gated behaviour as unverified rather than testing it.
- Verified pre-run backup of `.qompack/` (or recorded initial absence).

**Steps**

1. Work in the session until a compaction is due, then trigger `/compact`. `[requires SP-17
   artifact]`
2. List `.qompack/checkpoints/` and read the last line of `.qompack/checkpoints/MANIFEST.jsonl`.
3. Read the checkpoint artifact named by that line (`NNNN.json`, four zero-padded digits).
4. End the daemon: wait for the idle exit, or terminate the `pid` in `.qompack/run/daemon.lock`.
5. Run `qompack status` to bring a daemon back, then repeat steps 2 and 3 and diff the results.

**Expected observable result**

- Step 2: a new artifact exists and the manifest's last entry names it with its `sha256`
  (`internal/paths/manifest.go`; the manifest is append-only and is the record `qompack fsck`
  re-hashes — SP-17 implemented `fsck`, which verifies the checkpoint tier read-only and, with
  `--repair --yes`, can append a MANIFEST line for a clean orphan without deleting anything).
- Step 3: the artifact carries its frontier and its references. Concretely, the fields named in
  `internal/checkpoint/types.go` are `encoded_segments` (what the frontier has consumed),
  `pointers.files` (path plus `hash`) and `pointers.tools` (`tool_use_id` plus `hash`), alongside
  `invariants`, `user_intent`, `eliminated`, `decisions`, `open_questions`, `current_work`,
  `sketch_refs` and `dropped`. Every pointer hash is a `sha256:<64 hex>` address into the object
  store, not inlined content ([docs/architecture.md §7](architecture.md#7-checkpoint-and-rehydration)).
- Step 5: the two reads are identical. The checkpoint is on disk; a daemon restart re-reads it
  rather than regenerating it.
- **The explicit incomplete outcome.** When the newest checkpoint does not verify, the shipped
  resolver falls back and says so: `checkpoint.Resolution` carries `FellBack`, the `Skipped`
  sequence numbers and a one-line `Reason` formatted as `checkpoint NNNN does not verify; rolled
  back to NNNN`, and `ChainResolution` carries `Truncated` and `MissingFrom` for a broken ancestry
  (`internal/checkpoint/resolve.go`). **Nothing in this build calls either function** — a repository
  scan finds callers only in that package's own tests — so the *user-visible* form of an incomplete
  outcome is unknown. To settle it: corrupt one checkpoint artifact in a disposable project, run a
  rehydration, and record what the payload and `.qompack/logs/` say; if neither names the fallback,
  that is the finding.

**Evidence to record**

`uat/UAT-03/`: the checkpoint artifacts and `MANIFEST.jsonl` before and after the restart, the two
`qompack status` captures, `.qompack/logs/qompack-YYYYMMDD.log`, and — for the incomplete-outcome
probe — the corrupted artifact, the resulting payload and the log.

**Failure and rollback outcome**

A **fail** is: no checkpoint artifact after a compaction; an artifact whose manifest hash does not
match it; a post-restart read that differs from the pre-restart one; or an unverifiable checkpoint
that is replaced by an older one **without** any statement that the fallback happened. A **skip** is:
no host session to compact, leaving durable-checkpoint recovery unverified; the gated durable
frontier is unverified either way and the row says so.

Rollback: stop the verified disposable-project writer and follow the shared backup/restore
procedure above. Preserve the post-run store and later writes; restore the named backup into a
fresh recovery project and validate its matching project-file/configuration snapshot. When the
initial state was absent, retain this run as evidence and use a fresh empty project. Record the
reader proof, integrity results and any missing cross-version or activation checks. Do not edit
stored records to reconcile a failure.

**Result**

```text
Result: fail — the incomplete-outcome probe met the row's fail criterion: with the newest
  checkpoint 0002 corrupted (one byte) in a restored copy, the rehydration was built from the older
  0001 and presented as current ("# Qompack rehydration — checkpoint 0001"; state seq 1, dropped
  [], degraded false; no section 7), with no statement in the payload, the state or the drop
  report that a fallback happened; the logs say only "checkpoint artifact does not match its
  MANIFEST digest; the checkpoint is refused artifact=0002.json" and "checkpoint manifest
  mismatch", never that 0001 took its place ("checkpoint 0002 does not verify; rolled back to
  0001" appears nowhere). This is the first live run with an older checkpoint to fall back to.
  The round trip itself passes, and candidate 3's findings are fixed: after two real "/compact"
  turns 0001 and 0002 exist and the manifest's last line names 0002 with a matching sha256; the
  artifacts carry encoded_segments [1]/[2] and pointers.files/pointers.tools with sha256
  addresses; after the DEFAULT idle exit (1800 s, no override, no termination) no segment names an
  unwritten checkpoint and fsck --json (and --seal-check) exits 0; `qompack status` brought a daemon
  back, saying "no daemon answered: none is listening for this project yet. This command asked one
  to start", and the re-read of the listing, manifest and both artifacts is byte-identical. The
  gated durable frontier is unverified by this row.
  Candidate 3 (d5598eb4): pass — with findings: empty checkpoint pointers, restore integrity
  FAILED after a clean idle exit (segments named an unwritten 0002), evidence
  plans/sdd/V6-closeout/live/uat/UAT-03/
Snapshot: qompack version 0.3.0; bundle BUNDLE.json sha256
  aa7da0e17b7597562a6eba47fc48f1db81ff5e9bdc494b9997a323137625558d; commit
  9f6a2fadf086eba8080af589a35dd9554ae6cab4; Windows 11 Home 25H2 build 10.0.26200.9457;
  Claude Code 2.1.280
Date: 2026-09-29 (America/Toronto)
Executed by: Claude Code workflow subagent (Opus 5.5), agent-executed on the owner's real host
  per owner decision D3 — not human UAT
Evidence: plans/sdd/V6-closeout/live/rerun-c4/UAT-03/ (notes.txt indexes it; also C4.3)
Rollback verified: initial state absent (recorded); the run is retained as evidence. With the
  daemon gone by its default idle exit, backup uat03-after-idle was created (exit 0, consistent)
  and verified (exit 0, scratch restore and integrity passed) and restored into a fresh
  destination: exit 0, same-build reader proof OK (12 content roots, 9 tool refs, 0 tombstoned),
  integrity all ok including the dual-reader seal check, fsck of the destination exit 0; the
  source's later writes untouched; the destination was not activated; cross-version and
  activation checks unverified
```

---

## UAT-04

**Scenario**

Compaction belongs to the host. A manual `/compact` proceeds, an automatic compaction proceeds, and
a compaction that fails leaves the last usable checkpoint intact. Qompack never vetoes one, and
never claims it made the host's own input smaller.

**Preconditions**

- A session long enough to reach both a manual and an automatic compaction. `[requires SP-17
  artifact]`
- Configuration: `runtime.migration.compaction.automaticVeto` is `false` (its gate — the
  recovery/proactive distinction — has not passed) and
  `runtime.migration.compaction.blockManualCompact` is `false` and **hardwired** so: the key "exists
  only to say so" ([Gated switches](config-reference.md#gated-switches-ship-off),
  [docs/config-reference.md](config-reference.md#runtime)).
- `scheduler.hardCeilingMargin` and `scheduler.softFloorPct` at their defaults, so the scheduler's
  own pacing is the shipped one.
- Verified pre-run backup of `.qompack/` (or recorded initial absence).

**Steps**

1. Run `/compact` by hand in the session. `[requires SP-17 artifact]`
2. Record that the host compacted: the session continues, and a `PreCompact` checkpoint appears per
   UAT-03 step 2.
3. Continue working until the host's own automatic compaction fires, and record the same two
   observations. `[requires SP-17 artifact]`
4. Induce a failed compaction — the simplest reachable form is to make the `PreCompact` hook fail,
   for example by leaving the project config unparseable so the capture loader refuses
   (`{"runtime":`, one of the refusals recorded in
   [docs/troubleshooting.md](troubleshooting.md#6-configuration-and-schema-compatibility); an
   unknown key no longer does it, because it only warns) — then compact again.
   `[requires SP-17 artifact]`
5. After the failure, read `.qompack/checkpoints/MANIFEST.jsonl` and confirm the previous checkpoint
   is still the latest verifying one.
6. Search every Qompack output captured in this scenario for a claim that the host's input, prompt
   or native context was reduced.

**Expected observable result**

- Steps 1 and 3: the compaction happens. There is no veto path in this build to stop it — no
  supported native compaction request or veto exists at all
  ([docs/cannot-do.md](cannot-do.md#no-native-compaction-requestveto)).
- Step 4: the hook prints `{}` and exits `0` whatever happened inside it — a hook entry point is the
  only kind of subcommand that always exits `0` (`internal/cli/dispatch.go`, and the probed
  behaviour in [docs/troubleshooting.md](troubleshooting.md#6-configuration-and-schema-compatibility)).
  A failed compaction is therefore silent on stdout by design; the log is where it is visible.
- Step 5: the manifest's newest entry is the one from step 2 or 3, unchanged, and its artifact still
  hashes to the recorded `sha256`. Nothing was rewritten to make room for a checkpoint that was not
  written.
- Step 6 finds nothing. `custom_instructions` is `PreCompact` **input**, not a summarizer setter —
  the key that once meant otherwise is a retired meaning
  ([Retired-meaning keys](config-reference.md#retired-meaning-keys)) — and the rehydration budget is
  a cap on Qompack-added material, never a statement about restored native context
  ([docs/cannot-do.md](cannot-do.md#the-rehydration-budget-is-qompack-added-material-not-restored-native-context)).
  Any sentence in any output that reads otherwise is a **fail**.

**Evidence to record**

`uat/UAT-04/`: the session transcript around each compaction, the manifests before and after each
step, `.qompack/logs/qompack-YYYYMMDD.log` spanning the whole run, the failed hook's stdout and exit
status, and the text search from step 6 with its command line.

**Failure and rollback outcome**

A **fail** is: a compaction that does not proceed while Qompack is installed; a checkpoint manifest
that loses or rewrites its previous entry after a failed compaction; any native-shrink claim. A
**skip** is: no host session, leaving veto-freedom and the failed-compaction outcome unverified.

Rollback: stop the verified disposable-project writer and follow the shared backup/restore
procedure above. Preserve the post-run store and later writes; restore the named backup into a
fresh recovery project and validate its matching project-file/configuration snapshot. When the
initial state was absent, retain this run as evidence and use a fresh empty project. Record the
reader proof, integrity results and any missing cross-version or activation checks. Do not edit
stored records to reconcile a failure.

**Result**

```text
Result: pass — every compaction proceeded: 2 manual "/compact" and 14 automatic (forced with
  CLAUDE_CODE_AUTO_COMPACT_WINDOW=100000 and CLAUDE_AUTOCOMPACT_PCT_OVERRIDE=30), one host-failed
  automatic compaction (compact_error too_few_groups, retried successfully; its checkpoint 0001
  stays intact), and one with the PreCompact hook failing on an unparseable project config
  (`{"runtime":`): that hook printed {} and exited 0, the compaction proceeded, no checkpoint was
  written, and MANIFEST.jsonl kept its 16 lines with every artifact re-hashing (newest 0016 from the
  last automatic compaction). The step-6 search over 112 Qompack outputs found no native-shrink
  claim. Candidate 3's whole-record defect is fixed: with a 16,823-character first prompt every one
  of the 15 compact injections (841-2,498 UTF-16 units, inline) carries no part of it and names it
  first in section 7 as "user_intent tier1 — OVERFLOW: the verbatim original user intent did not
  fit the rehydration payload and is emitted whole or not at all; restore:
  expand(tool_use_id=prompt_…_0)" (state degraded true), no mid-record cut anywhere, and LOUD.log
  has no intent_mismatch line. Findings: the forced threshold made the host thrash again ("Autocompact
  is thrashing", two turns ended before the model answered, although expand had returned both the
  brief's closing marker and the deleted key's value); with 7 later prompts the three oldest
  evolution deltas were dropped by their 10% share (named, with pointers) while the payload used
  599 of 12,000 tokens; LOUD.log carries "rehydrate: tier-1 material exceeds the hard budget cap"
  on every compaction (15 lines) although section 7 already names the overflow (D50: logged once
  per session); after the run, with the planted unparseable config still in place, fsck exits 1
  on index.files "index/files.json is absent while its log carries 4 path(s)" (candidate 3 saw
  the same row; whether the planted config causes it was not isolated; under investigation).
  Candidate 3 (d5598eb4): pass — with findings: the 17,774-character first prompt was injected cut
  at 8,192 bytes mid-word with a spurious intent_mismatch, evidence
  plans/sdd/V6-closeout/live/uat/UAT-04/
Snapshot: qompack version 0.3.0; bundle BUNDLE.json sha256
  aa7da0e17b7597562a6eba47fc48f1db81ff5e9bdc494b9997a323137625558d; commit
  9f6a2fadf086eba8080af589a35dd9554ae6cab4; Windows 11 Home 25H2 build 10.0.26200.9457;
  Claude Code 2.1.280
Date: 2026-09-29 (America/Toronto)
Executed by: Claude Code workflow subagent (Opus 5.5), agent-executed on the owner's real host
  per owner decision D3 — not human UAT
Evidence: plans/sdd/V6-closeout/live/rerun-c4/UAT-04/ (notes.txt indexes it; also C4.3)
Rollback verified: not applicable — initial state absent (recorded); the run is retained as
  evidence; no backup or restore was run in this row
```

---

## UAT-05

**Scenario**

After a compaction, the one block Qompack injects fits inside the budget it was given, keeps the
current authority above the superseded material, and says out loud when something could not be
carried at all.

**Preconditions**

- A session that has compacted at least once. `[requires SP-17 artifact]`
- Configuration: `runtime.rehydrate.minTokens` and `runtime.rehydrate.maxTokens` at their defaults
  (`8000` and `12000`), `runtime.rehydrate.eliminationsTopN` at `8`,
  `runtime.migration.reinjection.sessionStartCompact` at `true` — the injection kill switch stays on
  for this row.
- Work in the session that produces a **correction**: state a requirement, then explicitly correct
  it, so the later statement supersedes the earlier one.
- Verified pre-run backup of `.qompack/` (or recorded initial absence).

**Steps**

1. Do the work, including the correction, then compact. `[requires SP-17 artifact]`
2. Capture the injected block from the resumed session's context. `[requires SP-17 artifact]`
3. Read `.qompack/state/rehydrate-<session>.json`, the persisted rehydration state for that session.
4. Run `/qompack:dropped --json` and capture the envelope.
5. Repeat the run with a deliberately tiny budget to force an overflow, and capture the same three
   artifacts. The rehydration is built by the project's daemon under the daemon's own
   configuration, so set the budget where the daemon reads it: both `runtime.rehydrate.minTokens`
   and `runtime.rehydrate.maxTokens` low in `<project>/.qompack/config.json`, then make sure the
   daemon that answers the compaction was started after that edit — end the running one (its idle
   exit, or the `pid` in `.qompack/run/daemon.lock`,
   [docs/troubleshooting.md](troubleshooting.md#7-daemon-problems)) so the next
   `session-start` starts one under the new file. A `--set` on a hook invocation does not change
   the budget: it configures that hook process only, and a daemon the hook starts is not given it.
   (A `QOMPACK_RUNTIME__REHYDRATE__MINTOKENS` / `QOMPACK_RUNTIME__REHYDRATE__MAXTOKENS` pair in
   the environment Claude Code runs hooks in also reaches a daemon a hook spawns, since it
   inherits that environment; the config file plus a daemon restart is the primary route.)

**Expected observable result**

- The block is delimited by `<!-- qompack:injected seq=N ver=V -->` and `<!-- /qompack:injected -->`
  (`internal/checkpoint/inject.go`), and its sections are the fixed order 1 Invariants, 2 Original
  user intent, 3 Approaches already eliminated, 4 Decisions, 5 Current work, 6 Pointers, 6a Restored
  instructions, 6b Skill index, 7 No longer in context, 8 Retrieval
  (`internal/rehydrate/render.go`; the exact heading strings are in that file).
- Its size is within the budget. The budget is a hard cap that is never raised — a caller's budget
  may be lowered, never raised, not even to the configured minimum
  ([ADR 0011](adr/0011-rehydration-budget-and-item-order.md)). `Tokens` and `Budget` in the state
  file from step 3 are the numbers to compare.
- **It arrives inline.** The whole compact `additionalContext` — the block plus the
  `<!-- qompack-contract-probe … -->` line after it — is at most 9,500 characters (UTF-16 code units,
  what the host counts), so the resumed session sees the block itself and never a
  `<persisted-output>` note with a saved-file path and a 2,000-character preview
  ([ADR 0011 §21](adr/0011-rehydration-budget-and-item-order.md)). Each record in the block is whole;
  what did not fit is named in section 7 with the call that restores it.
- **Current authority.** Section 2's evolution deltas are emitted newest-first, so a tight budget
  keeps the latest correction and drops the oldest restatement rather than the reverse
  (`internal/rehydrate/items.go`). The corrected requirement must appear above the superseded one.
  Note what the payload does **not** do: it prints no per-line authority label. The authority
  vocabulary — `user_correction` above `explicit_decision`, `tool_observation`,
  `candidate_extraction`, `hypothesis`, `conflict` — is `internal/core/evidence.go`'s and is read
  through records, not through the injected text
  ([docs/user-guide.md](user-guide.md#current-authority-corrections)). If the checkpoint's own
  `user_intent.original` disagrees with the L0 capture, the L0 text wins and a drop entry of kind
  `intent_mismatch` records the disagreement.
- **Overflow is explicit.** In step 5 the drop report names what could not be represented. Two
  shapes exist and both are recognized by `Overflowed` (`internal/rehydrate/budget.go`): an entry
  with kind `overflow` and id `payload`, whose detail begins `OVERFLOW: the injection wrapper alone
  (… tokens) exceeds the rehydration budget (… tokens); nothing was injected`; and a tier-1 entry
  whose id is `tier1`, whose detail begins `OVERFLOW:`, names the record (for example `pinned
  invariant inv_…` or `the verbatim original user intent`), says it `is emitted whole or not at all`
  and ends with the pointer that restores it (`restore: expand(tool_use_id=prompt_…_0)`, or `restore:
  Read .qompack/checkpoints/NNNN.json (…)`). A tier-1 entry sorts first in section 7. The state
  file's `degraded` field is `true` in both.
  Section 7 may itself be cut to the counted tail `- … and N more; call dropped()`, while the
  complete report stays in the state file and is what step 4 returns.
- Nothing in the block claims the host's restored context was reduced.

**Evidence to record**

`uat/UAT-05/`: the injected block for both runs (normal and tiny budget), both
`.qompack/state/rehydrate-*.json` files, both `/qompack:dropped --json` envelopes, and the session
transcript showing the correction.

**Failure and rollback outcome**

A **fail** is: a block larger than the budget; a compact `additionalContext` over 9,500 characters,
or one the session receives as a saved-file path and preview; a record cut mid-record; an older
statement rendered above the correction that superseded it; an overflow that appears nowhere — no `overflow`/`tier1` entry, no `degraded`, no
counted tail — while content is missing. A **skip** is: no host session, leaving budget adherence and
authority order unverified end to end.

Rollback: stop the verified disposable-project writer and follow the shared backup/restore
procedure above. Preserve the post-run store and later writes; restore the named backup into a
fresh recovery project and validate its matching project-file/configuration snapshot. When the
initial state was absent, retain this run as evidence and use a fresh empty project. Record the
reader proof, integrity results and any missing cross-version or activation checks. Do not edit
stored records to reconcile a failure.

**Result**

```text
Result: fail — under D50 the expectation is read literally: section 2 renders the superseded
  semicolon original above the TAB correction that supersedes it. Run 1 (a real session): the
  requirement "semicolon", a /qompack:pin made with the daemon running, the correction "must be a
  TAB character, not a semicolon ... superseded", record_eliminated (answered before any
  compaction), then "/compact". The block (3,093 UTF-16 units, 938/12,000 tokens, inline) has the
  delimiters and the fixed section order, carries the pin in section 1, the verbatim original and
  then, below it, the TAB correction in section 2's newest-first evolution (third, under two later
  ordinary prompts), the eliminated semicolon approach in section 3 and its decision in section 4;
  state dropped [], degraded false; /qompack:dropped --json {"count":0,"drops":[]};
  pins/invariants.json matches the log and fsck exits 0 with pins ok (no stale view);
  after compaction the model answered the delimiter "From the SessionStart hook's 'Original user
  intent' section". Step 5 ran as a hook invocation, not a second session: `qompack session-start`
  with a source=compact payload for run 1's session, the project config setting both bounds to 150,
  and a daemon started under it (the running one was terminated for this step, after checking its
  lock pid, image and command line): 362 units, 109/150 tokens, section 1 (the pin) and section 7
  cut to "- … and 14 more; call dropped()", state degraded true with the tier1 entry first
  ("OVERFLOW: the verbatim original user intent did not fit ... emitted whole or not at all;
  restore: expand(tool_use_id=prompt_…_0)"), dropped() returns all 14. No native-shrink claim.
  Findings: the step's "--set runtime.rehydrate.maxTokens on a hook invocation" changed nothing (the
  daemon's config sets the budget); a config change the daemon logs as "config reloaded
  changed=[runtime.rehydrate.maxTokens runtime.rehydrate.minTokens]" is not applied to its
  rehydration until a restart; at 150 tokens section 8 (retrieval) is evicted while the invariant
  stays, against ADR 0011's tier-1 admission order.
  Candidate 3 (d5598eb4): fail — the correction never reached a checkpoint or block, a pin made with
  the daemon up was missing (stale view), evidence plans/sdd/V6-closeout/live/uat/UAT-05/
Snapshot: qompack version 0.3.0; bundle BUNDLE.json sha256
  aa7da0e17b7597562a6eba47fc48f1db81ff5e9bdc494b9997a323137625558d; commit
  9f6a2fadf086eba8080af589a35dd9554ae6cab4; Windows 11 Home 25H2 build 10.0.26200.9457;
  Claude Code 2.1.280
Date: 2026-09-29 (America/Toronto)
Executed by: Claude Code workflow subagent (Opus 5.5), agent-executed on the owner's real host
  per owner decision D3 — not human UAT
Evidence: plans/sdd/V6-closeout/live/rerun-c4/UAT-05/ (notes.txt indexes it; also C4.3)
Rollback verified: initial state absent (recorded); the post-run store is retained; no backup or
  restore was run in this row; cross-version and activation checks unverified
```

---

## UAT-06

**Scenario**

Compact, resume, fork and correct repeatedly. No superseded intent is ever promoted back into the
current one, and nothing in the flow waits for a post-compaction event the host does not provide.

**Preconditions**

- A session with a stated original intent and at least one explicit correction. `[requires SP-17
  artifact]`
- Configuration: defaults, `runtime.migration.reinjection.sessionStartCompact` at `true`.
- Verified pre-run backup of `.qompack/` (or recorded initial absence).

**Steps**

1. State the original intent, work, correct it, then compact. `[requires SP-17 artifact]`
2. Resume the session and capture the injected block. `[requires SP-17 artifact]`
3. Compact a second time and capture the block again.
4. Fork the session (start a new one from the same project) and capture the block there. `[requires
   SP-17 artifact]`
5. Correct the requirement a second time, compact, and capture the block again.
6. For each captured block, record section 2's contents in order, and run `/qompack:why <id>` on any
   decision id the block lists.
7. Search `.qompack/logs/` for anything waiting on, or reporting the absence of, a post-compaction
   event.

**Expected observable result**

- Section 2's first unit is the **verbatim original intent from L0 capture**, never a summary: the
  heading says so, and the original is resolved by derived id from the verbatim first prompt rather
  than by relevance search ([docs/user-guide.md](user-guide.md#current-authority-corrections)).
- Across steps 2, 3, 4 and 5, the newest correction stays above the older ones and no superseded
  restatement is ever promoted to the top. Repeat compaction, resume and fork do not reorder it.
- `/qompack:why` returns an attributed record of a decision — what, why, the alternatives rejected
  and the evidence — and is **not** a claim about what the model then did
  ([docs/cannot-do.md](cannot-do.md#no-proof-of-model-compliance-with-injected-material)).
- Step 7 finds nothing: there is no PostCompact dependency anywhere in the flow. The one tested
  injection adapter is `SessionStart` with `source=compact`
  ([docs/cannot-do.md](cannot-do.md#no-postcompact-dependency)). If a log line shows the flow
  blocked on such an event, that is a **fail**, not a configuration problem.
- A second identical capture after a fork may legitimately differ in sequence numbers and
  timestamps. Whether any other field differs is **to be confirmed at execution**; record the diff
  rather than judging it.

**Evidence to record**

`uat/UAT-06/`: the four injected blocks as separate files, the `/qompack:why` responses, the session
transcripts, `.qompack/logs/qompack-YYYYMMDD.log` for the whole run, and the step-7 search command
with its output.

**Failure and rollback outcome**

A **fail** is: an obsolete intent appearing as the current one after any compact, resume or fork; a
summary-derived original intent; any dependency on a post-compaction event. A **skip** is: no host
session — leaving repeated-cycle intent stability unverified.

Rollback: stop the verified disposable-project writer and follow the shared backup/restore
procedure above. Preserve the post-run store and later writes; restore the named backup into a
fresh recovery project and validate its matching project-file/configuration snapshot. When the
initial state was absent, retain this run as evidence and use a fresh empty project. Record the
reader proof, integrity results and any missing cross-version or activation checks. Do not edit
stored records to reconcile a failure.

**Result**

```text
Result: fail — at step 4 the fork's block does not carry the only correction then in force: its
  section 2 keeps the parent's verbatim original ("allow 100 requests per minute per client") but
  the correction "must be 60 ..., not 100" is left out by the evolution share and named only in
  section 7 ("user_intent_evolution 0 — did not fit the rehydration budget; restore: Read
  .qompack/checkpoints/0004.json (user_intent.evolution[0])") while the block is 2,652 of 9,400
  characters, and the parent's session-scoped elimination of "100" and its decision are not
  inherited (no section 3 or 4), so the correction record itself is absent: 60 appears only inside
  an echoed record_eliminated prompt in the evolution list ('reason "superseded by the user's
  correction: 60 per minute"'), and "the newest correction stays above the older ones" does not
  hold there. Fixed since candidate
  3: the fork's section 2 is the PARENT's original ("(forked session: the original request of
  session c8466b7e, which this session continues)") with a section-7 fork-provenance entry and an
  expand pointer, and corrections now reach the checkpoints: blocks 1 and 2 carry the 60 correction
  under the original, block 4 carries the second correction (45) at the top. Steps run: original
  intent + correction + record_eliminated + compact (block 1); --resume (SessionStart:resume
  injected only the contract probe) + second compaction (block 2, which lost section 4: the
  decision is missing from checkpoint 0002 although its elimination is carried); fork (the fork's
  SessionStart injected only the probe) + compact (block 3); second correction + compact (block
  4); none skipped. /qompack:why dec_991dbff588ec returned an attributed record (what, why, the
  rejected alternative, evidence, checkpoint_seq 1), not a claim about the model. Step 7: no log
  line waits on or reports a post-compaction event. Fork diff (to be confirmed at execution):
  besides seq, checkpoint number and session id, section 2 gains the fork line and the fork's own
  first prompt and loses the oldest delta (the 60 correction), sections 3-4 disappear, current
  work and pointers are the fork's, and section 7 appears. MANIFEST skips seq 3; fsck exit 0.
  Candidate 3 (d5598eb4): fail — the fork's "verbatim original" was the fork's own first prompt
  and no correction ever reached a checkpoint, evidence plans/sdd/V6-closeout/live/uat/UAT-06/
Snapshot: qompack version 0.3.0; bundle BUNDLE.json sha256
  aa7da0e17b7597562a6eba47fc48f1db81ff5e9bdc494b9997a323137625558d; commit
  9f6a2fadf086eba8080af589a35dd9554ae6cab4; Windows 11 Home 25H2 build 10.0.26200.9457;
  Claude Code 2.1.280
Date: 2026-09-29 (America/Toronto)
Executed by: Claude Code workflow subagent (Opus 5.5), agent-executed on the owner's real host
  per owner decision D3 — not human UAT
Evidence: plans/sdd/V6-closeout/live/rerun-c4/UAT-06/ (notes.txt indexes it; also C4.3)
Rollback verified: not applicable — initial state absent (recorded); the run is retained as
  evidence; no backup or restore was run in this row
```

---

## UAT-07

**Scenario**

Find historical material four ways — by content, by path, by symbol, by handle — and get it back. A
thing that cannot be reached says `unavailable`; it never says `absent`, and it is never quietly
replaced with whatever the file says now.

**Preconditions**

- A session that has read and edited a source file with a named symbol in it. `[requires SP-17
  artifact]`
- Configuration: `retrieval.defaultSpan` at `minimal`, `runtime.mcp.spanWidenLines` at `40`,
  `runtime.mcp.maxResponseBytes` at `262144`, `store.retention.days` and `store.retention.sessions`
  at their defaults so nothing is collected mid-run.
- Verified pre-run backup of `.qompack/` (or recorded initial absence).

**Steps**

1. In the session, `/qompack:recall <phrase from the file> --json`.
2. `/qompack:recall path:<glob matching the file> --json`.
3. `/qompack:recall symbol:<the symbol name> --json`, and `/qompack:recall tool:<ToolName> --json`.
4. Call `expand` with the `hash` from a hit, then call `expand` again with the hit's `tool_use_id`.
5. Call `re_read` for the file with no `at`, then with `turn:<N>` for an earlier turn.
6. Edit the file on disk so its current contents differ from the captured version, then repeat step
   5's first call.
7. Call `expand` with a `sha256:` hash that was never stored, and record the response.

**Expected observable result**

- Steps 1–3: hits carry references and summaries rather than bytes, with `count` and `found`. A
  search that matched nothing is an answer, not a failure: the slash command exits `0`
  ([docs/user-guide.md](user-guide.md#qompackrecall)). The three selectors are exactly
  `path:<glob>`, `symbol:<name>` and `tool:<ToolName>`
  ([docs/mcp-tools.md](mcp-tools.md#recall)).
- Step 4: both handles resolve to the same content. The response carries the `hash` it resolved, the
  `span` it returned, `total_bytes`, `truncated`, `next_span` where there is more, and
  `_meta.qompack.ephemeral` — and **no** `source` field, because `expand` is addressed by handle and
  not by a point in a file's history ([docs/user-guide.md](user-guide.md#current-vs-historical-reads)).
- Step 5: the `re_read` response names `source: store` — the only value the field ever takes —
  beside the `at` it resolved and the `turn` the version was captured at.
- Step 6 is the substitution check: the response is **still** the captured version, not the edited
  file. There is no live-disk path in any of the eight tools and none may be added; a current-file
  read is the host's own separately authorized operation
  ([docs/troubleshooting.md](troubleshooting.md#5-retrieval-that-looks-wrong)).
- Step 7: a miss comes back as `found: false` with what was searched, not as a tool error
  (`internal/mcp/handlers_span.go`). A lookup that *failed* — as opposed to one that found nothing —
  reports the outcome `unavailable`, which is never `absent`
  ([docs/troubleshooting.md](troubleshooting.md#4-denied-or-unavailable-evidence)). Which of the two
  a never-stored hash produces on a live daemon is **to be confirmed at execution**; record the
  response verbatim.

**Evidence to record**

`uat/UAT-07/`: every `--json` envelope and every MCP response as a separate file, the file before
and after the step-6 edit, and `.qompack/index/*.jsonl` at the end of the run.

**Failure and rollback outcome**

A **fail** is: step 6 returning the edited on-disk contents; a failed lookup reported as `absent`; a
handle from a hit that does not resolve. A **skip** is: no host session or no MCP client, leaving
exact/path/symbol/handle discoverability unverified.

Rollback: stop the verified disposable-project writer and follow the shared backup/restore
procedure above. Preserve the post-run store and later writes; restore the named backup into a
fresh recovery project and validate its matching project-file/configuration snapshot. When the
initial state was absent, retain this run as evidence and use a fresh empty project. Record the
reader proof, integrity results and any missing cross-version or activation checks. Do not edit
stored records to reconcile a failure.

**Result**

```text
Result: pass — none of the row's fail conditions occurred. Steps 1-3 in the session:
  `cobalt heron threshold`, `path:src/*.go`, `path:*.go`, `symbol:ReconcileBalances` and
  `tool:Read` each returned hits (references and summaries, `count`, `found`); the glob and the
  host tool name now match (tool:Read = tool:FileRead, 4 hits each after the run); a no-match
  `path:nothing/*.rs` is `count 0, found false`, exit 0. Step 4: expand by `hash` and by
  `tool_use_id` gave the same hash, span [0,838], total_bytes 838 and content, with no `source`;
  `_meta.qompack.ephemeral` true. Step 5: re_read with no `at` gave `source: store`, turn 7
  (driftLimit 40); `at: "turn:1"` gave turn 1 (driftLimit 25). Step 6: after the on-disk edit
  (driftLimit 7) re_read still returned the captured turn-7 version. Step 7 OBSERVED (to be
  confirmed at execution) for a never-stored hash, not a tool error: `{"found":false,"searched":
  "the root index, including every indexed root's chunk list","available":false,"reason":"no
  indexed root or chunk carries this hash, so its content provenance could not be established"}`.
  Observation: after the session's MCP calls, `path:` answers are filled by retrieval
  self-records (one for src/nope.go, a path that never existed), crowding the files out.
  Candidate 3 (d5598eb4): pass — with findings: `path:` was not a glob and `tool:` rejected host
  names, evidence plans/sdd/V6-closeout/live/uat/UAT-07/
Snapshot: qompack version 0.3.0; bundle BUNDLE.json sha256
  aa7da0e17b7597562a6eba47fc48f1db81ff5e9bdc494b9997a323137625558d; commit
  9f6a2fadf086eba8080af589a35dd9554ae6cab4; Windows 11 Home 25H2 build 10.0.26200.9457;
  Claude Code 2.1.280
Date: 2026-09-29 (America/Toronto)
Executed by: Claude Code workflow subagent (Opus 5.5), agent-executed on the owner's real host
  per owner decision D3 — not human UAT
Evidence: plans/sdd/V6-closeout/live/rerun-c4/UAT-07/ (notes.txt indexes it; the session is
  ../C4.4/session/, shared with C4.4)
Rollback verified: not applicable — initial state absent (recorded: `backup create` exit 1 "no
  existing store"); per the row's rule the run is retained as evidence; no restore was run
```

---

## UAT-08

**Scenario**

Record that an approach does not work, with the evidence and the files the reason rests on, and then
ask about it. The answer comes back `active` with the exact fields that let a reader judge it — not a
bare yes.

**Preconditions**

- A session with MCP access, in a project with at least two files the reason can depend on.
  `[requires SP-17 artifact]`
- Configuration: `eliminations.requireEvidence` at `true`, `eliminations.defaultScope` at `session`,
  `eliminations.staleResponse` at `flag`, `eliminations.rebuildOnStale` at `nextIdle`.
- `runtime.phase7.reuse.scopedCandidates` stays `false`: cross-session reuse is gated off, so this
  row is about a record within its own scope.
- Verified pre-run backup of `.qompack/` (or recorded initial absence).

**Steps**

1. Call `record_eliminated` with `target`, `approach`, `reason`, `scope: "session"` and a
   `depends_on` list naming two real project-relative paths.
2. Capture the acknowledgement.
3. Call `already_tried` with the same `target` and `approach`, and capture the response.
4. Call `already_tried` with a *different* approach against the same target, and capture it.
5. Read `.qompack/records/eliminations.jsonl` and confirm the record is on disk.

**Expected observable result**

- Step 2's acknowledgement carries `id`, the canonical `descriptor` the record was keyed under,
  `scope`, `evidence`, the resolved `depends_on`, `depends_on_unresolved` (paths that could not be
  resolved to a hash), `status`, and any `warnings` (`internal/mcp/handlers.go`). A path listed under
  `depends_on_unresolved` is not a staleness dependency: check the acknowledgement before relying on
  persistence ([docs/mcp-tools.md](mcp-tools.md#record_eliminated)).
- Step 3 returns `state: "active"` with the confirmation fields: `reason`, `evidence` (the content
  hash backing the elimination), `scope`, `recorded_at` (RFC 3339, UTC) and `depends_on`
  (`internal/mcp/tools.go`, `AlreadyTriedResult`). `stale_because` is absent while the record is
  active.
- Step 4 returns `state: "absent"` for an approach nothing was recorded against. An `absent` is
  assertable only with complete, fresh coverage; anything less is `uncertain` or `unavailable`
  ([ADR 0013](adr/0013-migration-contracts.md)). A response carrying `_meta.qompack.bloom_only` is an
  unbacked filter hit and is reported as `absent` with a note saying so — record which form arrived.
- Step 5's file contains the record. `records/` and `sketches/tried.bloom` are append-only.

**Evidence to record**

`uat/UAT-08/`: the four MCP responses as JSON, a copy of `.qompack/records/eliminations.jsonl`, and
the two files named in `depends_on` with their contents at record time.

**Failure and rollback outcome**

A **fail** is: an acknowledgement with no `id` or no `evidence` while `eliminations.requireEvidence`
is `true`; an `already_tried` `active` answer missing `reason`, `evidence`, `scope`, `recorded_at` or
`depends_on`; an `absent` returned for the approach that was just recorded. A **skip** is: no MCP
client, leaving the scoped claim and its confirmation fields unverified.

Rollback: stop the verified disposable-project writer and follow the shared backup/restore
procedure above. Preserve the post-run store and later writes; restore the named backup into a
fresh recovery project and validate its matching project-file/configuration snapshot. When the
initial state was absent, retain this run as evidence and use a fresh empty project. Record the
reader proof, integrity results and any missing cross-version or activation checks. Do not edit
stored records to reconcile a failure.

**Result**

```text
Result: pass — with no compaction before them, step 2's acknowledgement carried id,
  descriptor, scope `session`, evidence, depends_on (both paths resolved) and
  depends_on_unresolved []; step 3 answered `active` with reason, evidence, scope, recorded_at
  (2026-09-30T03:29:33Z, RFC 3339 UTC) and depends_on, no stale_because; step 4 answered a plain
  `{"state":"absent"}` (no bloom_only); step 5: records/eliminations.jsonl holds the record,
  stored with the calling session's id. The next checkpoint (0001) carries it in eliminated and
  its decision dec_e760c8bbf50a in decisions; the model found that id in the injected block and
  `why` answered it, as did `/qompack:why dec_e760c8bbf50a` in the resumed session. Session scope
  isolates: in a later, different session of a project whose earlier session had recorded a
  session-scoped and a project-scoped elimination, already_tried answered `absent` for the first
  and `active` for the second (run in the C4.4 project, because this project's record had gone
  stale in UAT-09 and a stale record no longer answers after a restart — UAT-09's finding).
  Candidate 3 (d5598eb4): fail — the elimination ledger was not present before the first
  compaction and session scope was stored as "", evidence plans/sdd/V6-closeout/live/uat/UAT-08/
Snapshot: qompack version 0.3.0; bundle BUNDLE.json sha256
  aa7da0e17b7597562a6eba47fc48f1db81ff5e9bdc494b9997a323137625558d; commit
  9f6a2fadf086eba8080af589a35dd9554ae6cab4; Windows 11 Home 25H2 build 10.0.26200.9457;
  Claude Code 2.1.280
Date: 2026-09-29 (America/Toronto)
Executed by: Claude Code workflow subagent (Opus 5.5), agent-executed on the owner's real host
  per owner decision D3 — not human UAT
Evidence: plans/sdd/V6-closeout/live/rerun-c4/UAT-08/ (notes.txt indexes it)
Rollback verified: not applicable — initial state absent (recorded: `backup create` exit 1 "no
  existing store"); the run is retained as evidence (the project continued into UAT-09); no
  restore was run
```

---

## UAT-09

**Scenario**

Change a file the elimination's reason rested on, or break the query, and the answer stops being
confident. A stale or unknown state is reported as such, and neither one is allowed to prohibit the
approach.

**Preconditions**

- UAT-08 complete in the same project, so a record with `depends_on` exists.
- Configuration: `eliminations.staleResponse` at `flag` for steps 1–3, then `drop` for step 4.
  `eliminations.rebuildOnStale` at `nextIdle`.
- Verified pre-run backup of `.qompack/` (or recorded initial absence) (take it before UAT-08 if the two rows share a project).

**Steps**

1. Edit one of the files named in the record's `depends_on`.
2. Call `already_tried` with the recorded `target` and `approach`, and capture the response.
3. Read the response's `stale_because` and confirm it names the file that changed.
4. Set `eliminations.staleResponse` to `drop`, repeat step 2, and capture the response.
5. Make the ledger unreadable to force a query failure, and capture the response. A project with no
   ledger yet does not do it: the daemon opens the ledger on the first `already_tried` or
   `record_eliminated` call and answers `absent`. The reachable form is a second disposable project
   whose `.qompack/records/eliminations.jsonl` is made unreadable before any session there starts —
   create it as an empty directory — then run step 2's query in a session in that project.
6. Record, for each of steps 2, 4 and 5, whether anything in the response prohibits the approach.

**Expected observable result**

- Step 2 returns `state: "stale"` with `stale_because` naming the changed `depends_on` entries, and
  the record's `reason`, `evidence`, `scope` and `recorded_at` still attached
  (`internal/mcp/handlers.go`). A record goes stale rather than being deleted
  ([docs/mcp-tools.md](mcp-tools.md#record_eliminated)).
- Step 4 returns `state: "uncertain"`. `drop` suppresses the staleness *detail* — the reason,
  evidence and `stale_because` — but may not claim absence: the handler has just been told an
  elimination exists (`internal/mcp/handlers.go`, `renderAnswer`). The exact `reason` and `note`
  strings it substitutes are **to be confirmed at execution**; they are package constants in that
  file.
- Step 5 returns `state: "unavailable"` with `degraded: true`, a `reason` saying what could not be
  established and a `note` giving the recovery direction. Nothing about a record is disclosed, and
  the answer is **unknown**, not absent ([docs/user-guide.md](user-guide.md#fidelity-coverage-and-error-states)).
- Step 6 finds no prohibition in any of the three. A failed `already_tried` tells you nothing about
  whether the approach was tried, and does not license retrying as though it were fresh ground
  either ([docs/troubleshooting.md](troubleshooting.md#4-denied-or-unavailable-evidence)).
- **Known gap, not a pass.** `plans/V5-report.md` §22 item 26 records that with the observation
  ledger blind, a digest can render an MCP elimination record `[active]` from a checkpoint's frozen
  copy while its real state is stale and `already_tried` reports unavailable. If the injected block
  shows `[active]` here while step 2 says `stale`, record it as that known partial — the
  authoritative answer is `already_tried`'s
  ([docs/troubleshooting.md](troubleshooting.md#5-retrieval-that-looks-wrong)).

**Evidence to record**

`uat/UAT-09/`: the three `already_tried` responses, the edited file before and after, the two config
files used for `flag` and `drop`, `.qompack/records/eliminations.jsonl`, and the injected block if
one was produced during the run.

**Failure and rollback outcome**

A **fail** is: a changed dependency that leaves the state `active`; a `drop` configuration that turns
staleness into `absent`; an `unavailable` without `degraded`; any response whose text forbids the
approach. A **skip** is: no MCP client, leaving stale and uncertain reporting unverified.

Rollback: stop the verified disposable-project writer and follow the shared backup/restore
procedure above. Preserve the post-run store and later writes; restore the named backup into a
fresh recovery project and validate its matching project-file/configuration snapshot. When the
initial state was absent, retain this run as evidence and use a fresh empty project. Record the
reader proof, integrity results and any missing cross-version or activation checks. Do not edit
stored records to reconcile a failure.

**Result**

```text
Result: fail — step 4: with `drop` loaded by a new daemon, already_tried in the resumed
  recording session (the same session id, the call filed under it) answered `{"state":"absent"}`
  for the stale record — the row's "drop configuration that turns staleness into absent". A second
  resume with `flag` also answered `absent`, so drop is not the cause: after a daemon restart a
  stale elimination is missing from the query filter, which is built from active records only
  (internal/negknow), and the query answers absent before reading the record. Reached and
  passing: steps 1-3 in the recording session — seconds after the model edited config/pool.yaml,
  already_tried answered `stale` with reason, note, evidence, scope, recorded_at, depends_on and
  stale_because ["config/pool.yaml: dependency hash changed from sha256:9f2751eac830"]; the
  injected block after the next compaction showed it `[stale: ...]` (known gap not reproduced).
  Step 5, with records/eliminations.jsonl made unreadable (created as a directory):
  `{"state":"unavailable","reason":"the elimination ledger is in blind mode:
  records/eliminations.jsonl could not be read","note":"repair or restore the elimination log and
  restart; this is not evidence the approach is untried","degraded":true}`, and record_eliminated
  a tool error. The step's "reachable form" (a project with no ledger yet) now answers a correct
  `absent`, because the ledger opens on first use: that sentence is out of date. Step 6: no
  response prohibits the approach. Step 4's reason/note strings were not reachable on this run.
  A `drop` written mid-session did not reach the running daemon's already_tried.
  Candidate 3 (d5598eb4): fail — staleness was not refreshed in the recording session and the
  no-ledger query never answered unavailable, evidence plans/sdd/V6-closeout/live/uat/UAT-09/
Snapshot: qompack version 0.3.0; bundle BUNDLE.json sha256
  aa7da0e17b7597562a6eba47fc48f1db81ff5e9bdc494b9997a323137625558d; commit
  9f6a2fadf086eba8080af589a35dd9554ae6cab4; Windows 11 Home 25H2 build 10.0.26200.9457;
  Claude Code 2.1.280
Date: 2026-09-29 (America/Toronto)
Executed by: Claude Code workflow subagent (Opus 5.5), agent-executed on the owner's real host
  per owner decision D3 — not human UAT
Evidence: plans/sdd/V6-closeout/live/rerun-c4/UAT-09/ (notes.txt indexes it; steps 1-3 in
  ../UAT-08/session1/)
Rollback verified: not applicable — initial state absent before UAT-08 (recorded: `backup create`
  exit 1 "no existing store"); the run is retained as evidence; no restore was run
```

---

## UAT-10

**Scenario**

Ask the installation what it observed about itself. The answer separates the usage categories it can
account for from the ones it cannot, prints a word rather than a number where no instrument exists,
and keeps its uncertainty visible instead of rounding it into a verdict.

**Preconditions**

- A project that has hosted at least one session, so there is something to report. `[requires SP-17
  artifact]`
- Configuration: defaults. `runtime.telemetry.enabled` is `false` and hardwired so — nothing is sent
  anywhere ([docs/troubleshooting.md](troubleshooting.md#2-unknown-capability-or-telemetry)).
- Verified pre-run backup of `.qompack/` (or recorded initial absence).

**Steps**

1. `/qompack:status --json`, captured, and `qompack status` in text form for the same moment.
2. Read the envelope's `primary` provenance and every row's own `provenance` and `measure`.
3. `/qompack:dropped --json`, captured.
4. `/qompack:eval --json`, captured, with its exit status.
5. Read `.qompack/logs/qompack-YYYYMMDD.log` and the `recent loud lines` section of the text status.
6. For any elimination the injected digest shows, cross-check it with `already_tried` per UAT-09.

**Expected observable result**

- Step 1's envelope is the versioned `{schema, command, ok, data}` shape
  ([docs/commands.md](commands.md#qompackstatus)). Its `data` carries `primary`, `snapshot`, `hooks`
  and `budgets`.
- Step 2: every displayed value carries provenance — a `source` (`daemon`, `disk` or `none`), a
  `status` (`available`, `unavailable`, `error`), an `age_ms` that is `null` when unknown rather than
  `0`, and a `measure` (`observed` or `estimated`)
  ([docs/user-guide.md](user-guide.md#qompackstatus)). A per-hook row with no instrument reads
  `unavailable` **with its reason**; the B-D budget row reads that no instrument records the
  histogram because it measures host process creation, which the created process cannot observe
  (`internal/commands/statuscollect.go`, quoted in
  [docs/troubleshooting.md](troubleshooting.md#2-unknown-capability-or-telemetry)).
- **Usage categories.** The six are `input`, `output`, `cache_read`, `cache_write_5m`,
  `cache_write_1h` and `thinking_output`, in that canonical order, and a volume that was not reported
  is *unknown*, not zero — an unknown count serializes as `{"known":false}` and the category is
  listed under `missing` (`internal/eval/ledger.go`). They are reported beside an evaluation's
  verdict, and only where an evaluation's artifacts exist: `qompack eval` reads a `devtool
  live-eval` run under `dist/live-eval/` or the replay report at `testdata/bench-replay.json`, both
  build outputs of a Qompack source checkout (`internal/commands/evalartifacts.go`). In a user
  project neither exists, so step 4 reports `unavailable` — `no evaluation artifacts`, naming both
  paths it looked in — and exits `1`. Recording that message and exit status is the pass; a report
  appearing here means the project holds evaluation artifacts, and the evidence must then record
  which run it read and whether it says `confirmatory: yes`.
- Steps 3 and 5: the drop report is Qompack's recorded omissions — each entry a `kind`, an `id` and
  an optional `detail`, qualified on the report as a whole by `available`/`reason` or
  `denied`/`host_policy`, with no coverage value — and it does not establish what remains in native
  context ([docs/mcp-tools.md](mcp-tools.md#dropped),
  [docs/user-guide.md](user-guide.md#qompackdropped)).
- Steps 3 and 5 expectation revised under D46 (2026-09-29).
- Step 6 is the uncertainty check, and it carries a **known gap, not a pass**:
  `plans/V5-report.md` §24 records the uncertainty gate as **partial** — it "does not survive the
  digest surface under a blind ledger" — and §29 item 11 records pin records stamped `mcp` because
  the ingest path has no production caller. Read an uncertainty marking in a digest as a claim that
  has not been verified end to end, and record the mismatch as the recorded partial
  ([docs/user-guide.md](user-guide.md#current-authority-corrections)).

**Evidence to record**

`uat/UAT-10/`: `status.json`, `status.txt`, `dropped.json`, `eval.json` with its exit status,
`.qompack/logs/qompack-YYYYMMDD.log`, `.qompack/logs/LOUD.log` if one exists, and the `already_tried`
responses from step 6.

**Failure and rollback outcome**

A **fail** is: a latency or budget cell printing `0` where no instrument exists; an `age_ms` of `0`
standing in for unknown; a usage figure presented without its category or its known/unknown flag;
any telemetry leaving the machine. A **skip** is: a project that has never hosted a session, which
leaves the accounting surface unverified — and note that "no assertions reported" from a project
that has hosted none is the honest answer, not a skip
([docs/troubleshooting.md](troubleshooting.md#1-start-with-provenance)).

Rollback: stop the verified disposable-project writer and follow the shared backup/restore
procedure above. Preserve the post-run store and later writes; restore the named backup into a
fresh recovery project and validate its matching project-file/configuration snapshot. When the
initial state was absent, retain this run as evidence and use a fresh empty project. Record the
reader proof, integrity results and any missing cross-version or activation checks. Do not edit
stored records to reconcile a failure.

**Result**

```text
Result: pass — no latency or budget cell printed 0 without an instrument (per-hook rows read
  `unavailable` with their reason, B-D says no instrument records it because it measures host
  process creation), `age_ms` was 0 only for a live daemon source and after the daemon stopped the
  text read "source: none (error, age unknown)", `/qompack:eval --json` exited 1 with
  `no evaluation artifacts` naming both `dist/live-eval` and `testdata/bench-replay.json`, and no
  telemetry is enabled. The status envelope is {schema, command, ok, data{primary, snapshot, hooks,
  budgets}}; dropped read {"count":0,"drops":[]}; the digest's `[active]` elimination agreed with
  `already_tried` (active). Usage categories: not reachable from a user project; via `qompack eval
  --corpus` on the committed C5.4 pilot run the six names appear only as arm totals
  {known, known_records, unknown_records} in alphabetical order, and cost reads "unavailable: no
  request ledger was recorded for this run", so the canonical order and the {"known":false} +
  `missing` form were not observed (finding). Other findings: the contract banner reads "1 of 9
  assertion(s) FAILING" (mcp.server_registered initialize-not-received) while MCP answered; p95/p99
  print above the max (106.50 ms vs max 99.00 ms); the refusal after a daemon stop has an empty
  reason; no "recent loud lines" section appears when the tail is empty.
Snapshot: qompack version 0.3.0; bundle BUNDLE.json sha256
  32600778ae6463cd47736fc6b0a8614ad782e5bbd0ef0440f4e2c3937ccf4505; commit
  d5598eb4445954120ee795560c2ea46640772f43; Windows 11 Home 25H2 build 10.0.26200.9457;
  Claude Code 2.1.280
Date: 2026-09-29 (America/Toronto)
Executed by: Claude Code workflow subagent (Opus 5.5), agent-executed on the owner's real host
  per owner decision D3 — not human UAT
Evidence: plans/sdd/V6-closeout/live/uat/UAT-10/ (notes.txt indexes it)
Rollback verified: not applicable — initial state absent (recorded: `backup create` exit 1 "no
  existing store"); per the row's rule the run is retained as evidence; no restore was run
```

---

## UAT-11

**Scenario**

With admission off — which is how this build ships — an input Qompack does not recognize, or a
capture that fails outright, leaves the original untouched and passes it through. Whatever pointers
do reach the injected block all resolve to something.

**Preconditions**

- A session with MCP access. `[requires SP-17 artifact]`
- Configuration: `runtime.migration.replacement.newResult` stays `false`. Replacing newly delivered
  tool results with Qompack handles is SP-21's gated capability and its M4 gate has not passed, so a
  `true` value is refused at load and recorded as a violation
  ([Gated switches](config-reference.md#gated-switches-ship-off)).
- `runtime.redact.enabled` at `true` and `runtime.migration.settingsVersion` at `1`, so the privacy
  policy and the schema version are the shipped ones.
- Verified pre-run backup of `.qompack/` (or recorded initial absence).

**Steps**

1. Confirm the gate is off: `qompack config print --provenance`, and read the `newResult` leaf.
2. Attempt to turn it on from a project config file, run a hook, and read
   `.qompack/state/config-violations.json`.
3. Restore the config file to `{}`, then force an unrecognized schema: set
   `runtime.migration.settingsVersion` to a value higher than this build understands, run a hook, and
   observe.
4. Force a capture failure: make the project config unparseable (`{"runtime":`), run a hook, run
   `qompack self-test`, and observe. Then replace it with an unknown key
   (`{"runtime":{"notAKey":1}}`), run a hook and `qompack self-test` again, and observe.
5. In a clean session, do work whose results are large or unusual, compact, and capture the injected
   block. `[requires SP-17 artifact]`
6. For every pointer in section 6 of that block, call `expand` with its hash and `re_read` with its
   path, and record whether each resolved.

**Expected observable result**

- Step 1: `"newResult": false`, origin `default` (observed on this tree in a defaults-only project).
- Step 2: the leaf still reads `false`, and `config-violations.json` gains an entry whose message is
  `invalid value, using default: true not in false` — a refused gated switch is recorded as an
  invalid value ([docs/troubleshooting.md](troubleshooting.md#1-start-with-provenance)).
- Step 3: the whole `runtime.migration` block is reset to defaults, so unknown future switches stay
  off, capture continues, and the hook records the reset in `config-violations.json` as well as in
  the day log ([Versioned blocks](config-reference.md#versioned-blocks),
  [docs/troubleshooting.md](troubleshooting.md#6-configuration-and-schema-compatibility)).
- Step 4 is the **pass-through** check, and the sharpest thing on this row. With the unparseable
  file the capture loader refuses — a layer that does not parse cannot be applied per leaf — so **no
  capture is admitted at all**: the hook still prints `{}` and exits `0`, `.qompack/` holds nothing
  but the config file in a fresh directory, and `self-test`'s `config.capture` row fails critically,
  exit 1, naming `the project config file is not a single strict JSONC object`. The original host
  payload is untouched: Qompack recorded nothing and replaced nothing. That is what pass-through
  means here, and it is within the privacy policy because nothing entered the store. With the
  unknown key, capture continues and `config.capture` is a warning that names the unknown key —
  both observed on this tree
  ([docs/troubleshooting.md](troubleshooting.md#6-configuration-and-schema-compatibility)).
- Step 6: every pointer resolves through `expand` (by `hash`) or `re_read` (by `path`). Section 6's
  own heading says contents are **not** restored and that `expand`/`re_read` are how you get them
  (`internal/rehydrate/render.go`). A pointer that resolves to nothing is a **fail**; a pointer whose
  content is `redacted` or `truncated` is **not** — that is a recorded fidelity, and the privacy
  policy applied at capture is why ([docs/troubleshooting.md](troubleshooting.md#3-capture-gaps)).

**Evidence to record**

`uat/UAT-11/`: each config file used, `config-violations.json` after step 2,
`.qompack/logs/qompack-YYYYMMDD.log` for steps 3 and 4, a directory listing of the project after each
hook run, the injected block, and the `expand`/`re_read` responses for every pointer.

**Failure and rollback outcome**

A **fail** is: a gated switch that takes effect; a capture written from a configuration the capture
loader refused; a modified or dropped host payload; a pointer that does not resolve. A **skip** is: no
host session for steps 5–6, leaving pointer resolution unverified — steps 1–4 are runnable today
against a locally built binary in a scratch directory.

Rollback: stop the verified disposable-project writer and follow the shared backup/restore
procedure above. Preserve the post-run store and later writes; restore the named backup into a
fresh recovery project and validate its matching project-file/configuration snapshot. When the
initial state was absent, retain this run as evidence and use a fresh empty project. Record the
reader proof, integrity results and any missing cross-version or activation checks. Do not edit
stored records to reconcile a failure.

**Result**

```text
Result: pass — steps 1-4 as written on the frozen binary: `"newResult": false // default`;
  the refused `true` stays false with `invalid value, using default: true not in false` in
  config-violations.json; settingsVersion 2 resets the whole runtime.migration block
  (config-violations.json, day log, LOUD.log) and capture continues; the unparseable file makes the
  hook print `{}` exit 0 with only config.json in the fresh .qompack/, and self-test exits 1 with
  config.capture critical, detail "... the project config file is not a single strict JSONC
  object"; the unknown key keeps capture on and config.capture warns `runtime.notAKey: unknown
  key`. Step 5: the first turn read the whole 363 KB big.log and the session was not degraded:
  hook.additional_context_delivered reads "sentinel-observed", and SessionStart:compact injected
  the block. Step 6: all 9 section-6 pointers resolved — the 4 file pointers by expand (hash) and
  re_read (path), the 5 tool_use pointers by expand (the model used their tool_use_ids, each
  returning the line's hash; the one hash not also expanded in-session resolved through a
  no-model probe). big.log is truncated with next_span and creds.env redacted: recorded
  fidelities. The planted secret is in no form in the store.
  Candidate 3 (d5598eb4): fail — one large first-turn Read hid the probe sentinel, the session
  was degraded to passive and nothing was injected, evidence plans/sdd/V6-closeout/live/uat/UAT-11/
Snapshot: qompack version 0.3.0; bundle BUNDLE.json sha256
  aa7da0e17b7597562a6eba47fc48f1db81ff5e9bdc494b9997a323137625558d; commit
  9f6a2fadf086eba8080af589a35dd9554ae6cab4; Windows 11 Home 25H2 build 10.0.26200.9457;
  Claude Code 2.1.280
Date: 2026-09-29 (America/Toronto)
Executed by: Claude Code workflow subagent (Opus 5.5), agent-executed on the owner's real host
  per owner decision D3 — not human UAT
Evidence: plans/sdd/V6-closeout/live/rerun-c4/UAT-11/ (notes.txt indexes it)
Rollback verified: not applicable — initial state absent (recorded: `backup create` exit 1 "no
  existing store"); per the row's rule the run is retained as evidence; no restore was run
```

---

## UAT-12

**Scenario**

The privacy and lifecycle edges: a read the policy refuses is refused before anything of it is shown;
a large or opaque object is decoded within a bound; and a backup taken before the first write can be
restored afterwards, with the restore verified — across an upgrade and an uninstall.

**Preconditions**

- A disposable project with a file the host is configured to deny, a file comfortably larger than
  `runtime.mcp.maxResponseBytes` (default `262144`), and a binary file.
- Configuration: `runtime.redact.enabled` at `true`, `runtime.mcp.maxResponseBytes` at its default,
  `store.retention.days` and `store.retention.sessions` at their defaults.
- **A verified pre-run backup is mandatory for this row.** Use the commands above with the
  source writer stopped. If no store exists, record that absence and take a non-vacuous backup
  after the initial permitted capture, before the upgrade. Keep the original later-write store
  and project-file snapshot; import/cutover remains separately gated.

**Steps**

1. Take and verify the pre-run backup, or record initial absence and the later baseline backup as described above.
2. Attempt to read the denied file through the session, then call `recall` and `expand` for it.
3. Call `expand` on the oversized file's capture, with and without `full: true`.
4. Call `expand` on the binary file's capture.
5. Record the sizes of every response in steps 3 and 4.
6. Upgrade the plugin to a newer bundle. `[requires SP-17 artifact]`
7. Run a hook and confirm the layout still appears; read the day log for version-block and
   retired-meaning warnings.
8. Stop the verified scenario writer and restore the named backup into a fresh recovery project
   using `backup restore`. Save its reader proof and integrity report, preserving the original
   post-run store and later writes. Separately validate the intended upgrade/rollback reader;
   same-build readback does not certify an older release. Missing compatibility evidence keeps
   that portion of recovery unverified.
9. Uninstall the plugin. `[requires SP-17 artifact]`
10. Confirm `<project>/.qompack/` is still on disk after the uninstall, and that deleting it is the
    only way to remove it.

**Expected observable result**

- Step 2: the outcome is `denied` — a policy or privacy decision refused this read; **something
  exists and you may not have it** — and no preview, summary or excerpt of the content accompanies
  it. `denied` is not `absent` and is not an empty read
  ([docs/troubleshooting.md](troubleshooting.md#4-denied-or-unavailable-evidence)). An archive is
  never a bypass for a denied read ([docs/cannot-do.md](cannot-do.md#5-trust-boundary)).
- Steps 3–5: every response is within `runtime.mcp.maxResponseBytes`. A minimal span is
  chunk-aligned and widened to a symbol boundary where one is known; `full: true` returns the whole
  *available* object; more to read is paged through `next_span`, and a cut response says so with
  `truncated: true` ([docs/mcp-tools.md](mcp-tools.md#expand)). Each response reports the read —
  `span`, `total_bytes`, `truncated`, `next_span` — and carries no fidelity or coverage field. A
  binary capture's fidelity is recorded on its capture sidecar, not in the response; its content
  is opaque, not to be searched or quoted as text
  ([docs/troubleshooting.md](troubleshooting.md#3-capture-gaps)).
- Steps 3–4 expectation revised under D46 (2026-09-29).
- Step 7: after an upgrade, a config file written by a newer build has its versioned block reset to
  defaults; a gated switch that was `true` in a build whose gate had passed is refused in one where
  it has not; a retired-meaning key is still applied with a deprecation warning naming the file and
  line. Read all three in the *new* build's reference
  ([docs/troubleshooting.md](troubleshooting.md#6-configuration-and-schema-compatibility)).
  `qompack self-test`'s `config.capture` row reading `ok` or `warn`, and the layout reappearing after
  a hook run, are the confirmation that capture still loads.
- Step 8: record the `qompack fsck` result and optional file comparison as diagnostic observations.
  Keep `Rollback verified` unverified unless the engine-supported backup, stable frontier,
  compatible-reader and later-write requirements in the shared recovery prerequisites are met.
- Step 10: `<project>/.qompack/` survives the uninstall with everything already recorded in it;
  removing the plugin stops it being invoked and deletes nothing
  ([docs/troubleshooting.md](troubleshooting.md#8-safe-disable)).
- The exact host behaviour for a denied read, and the exact upgrade warnings, are **to be confirmed
  at execution**: neither has been observed against an installed host on this tree.

**Evidence to record**

`uat/UAT-12/`: the named verified backup and manifest (the recovery artifact), every MCP
response with its byte size, the day log across the upgrade, the step-8 `qompack fsck` output and any
comparison commands and their full output, and directory listings before and after the uninstall.

**Failure and rollback outcome**

A **fail** is: any preview, excerpt or summary of a denied read; a response exceeding
`runtime.mcp.maxResponseBytes`; a binary object decoded as text; a restore whose reader proof or integrity checks fail; a `.qompack/` deleted or altered by an upgrade or an uninstall. A **skip** is: no upgrade or
uninstall path, which leaves the upgrade and uninstall halves unverified — steps 1–5 and 8 are
runnable without them, and a row that ran only those says so in its Result.

Rollback: stop the verified disposable-project writer and follow the shared backup/restore
procedure above. Preserve the post-run store and later writes; restore the named backup into a
fresh recovery project and validate its matching project-file/configuration snapshot. When the
initial state was absent, retain this run as evidence and use a fresh empty project. Record the
reader proof, integrity results and any missing cross-version or activation checks. Do not edit
stored records to reconcile a failure.

**Result**

```text
Result: pass — step 2: the host refused the direct Read ("File is in a directory that is denied
  by your permission settings." — observed string) and recall (marker and path:), expand
  (tool_use_id and root hash), re_read, /qompack:recall and a direct stdio probe all answered
  denied ("authorization denied: the host's current permission rules deny reading the associated
  path" — observed string) with no preview; out-of-project re_read refused ("path escapes the
  project root", no path echoed), expand of the outside capture denied. Steps 3-5: every expand
  and re_read result text is within runtime.mcp.maxResponseBytes 262,144 on a 354,352-byte
  escape-heavy capture: minimal 8,038 bytes (span [0,6437], next_span 6437:16384); full: true
  261,163 (span [0,217070], truncated true, next_span 217070:137282); that next_span passed back
  165,233 (span [217070,354352], contiguous, to the end); re_read full: true 261,172; an explicit
  span wins over full; no page ends inside a multi-byte character; each response reports span,
  total_bytes, truncated and next_span and carries no fidelity or coverage field (as revised
  under D46). Step 4: no binary bytes reach Qompack on this host — Bash `cat` delivers the
  3,000-byte file as 2,134 bytes of host-decoded text and Read delivers a PNG as a base64 image
  block; both capture sidecars record fidelity exact (the captured host delivery), expand returns
  what was captured, recall does not find the blob's magic; Qompack decoded nothing. Step 6:
  upgrade 0.2.99-prev (built from 301a8e9; the v0.2.0 tag has no bundle task) to 0.3.0 left
  .qompack/ byte-identical. Step 7 (to be confirmed at execution): no version-block or
  retired-meaning warning (no config file); no LOUD line at the first post-upgrade daemon start;
  config.capture "applied as written"; the layout reappeared after session B's hooks. Step 8:
  restore of the pre-upgrade backup exit 0, reader proof and integrity checks passed. Steps 9-10:
  .qompack/ byte-identical across the uninstall (791 files); after the reinstall re_read answered
  from the old build's capture. Findings (not fail criteria): a default k=5 recall answered 2 hits
  with denied 3 while more permitted hits existed (withheld hits use up k); later daemon starts
  logged LOUD "publication accounting incomplete" (250 ms startup bound); troubleshooting §3 still
  tells the reader to read fidelity through expand; F4: section 6 of the rehydration block lists
  pointers carrying the deny-ruled file's absolute path and root hash and the out-of-project
  file's absolute path, with no content (D50 routes it to a fix: pointers never show a denied path
  or an absolute path outside the project); paging semantics on candidate 4: the final page
  answers truncated true with no next_span, and an explicit span 0:354352 cut at 217070 answers
  next_span 217070:16384 where full: true answers 217070:137282 (fixed after candidate 4 under D50: a
  truncated page always carries next_span, and an explicit span pages like full: true). ORDER: steps 2-5 ran after step 6, on the
  candidate, because 301a8e9 predates the C1.9 deny-rule support; the baseline was taken after
  the old build's permitted captures, MCP calls and /compact (initial state absent).
  Candidate 3 (d5598eb4): fail — full: true responses of 263,559 / 263,567 bytes over
  the bound and a pre-upgrade restore failing its integrity checks, evidence
  plans/sdd/V6-closeout/live/uat/UAT-12/
Snapshot: qompack version 0.3.0; bundle BUNDLE.json sha256
  aa7da0e17b7597562a6eba47fc48f1db81ff5e9bdc494b9997a323137625558d; commit
  9f6a2fadf086eba8080af589a35dd9554ae6cab4 (upgraded from 0.2.99-prev built from 301a8e9);
  Windows 11 Home 25H2 build 10.0.26200.9457; Claude Code 2.1.280
Date: 2026-09-29 (America/Toronto)
Executed by: Claude Code workflow subagent (Opus 5.5), agent-executed on the owner's real host
  per owner decision D3 — not human UAT
Evidence: plans/sdd/V6-closeout/live/rerun-c4/UAT-12/ (notes.txt indexes it; C4.8 notes under
  plans/sdd/V6-closeout/live/rerun-c4/C4.8/)
Rollback verified: unverified — backup uat12-c4-baseline (136 files, consistent, frontier 0)
  created and verified by the candidate CLI with the source daemon stopped; same-build restore
  into a fresh destination proved its reader (27 roots, 19 tool refs) and passed its integrity
  checks and the delivery seal check (fsck --seal-check of the recovery and of the source exit
  0); the source's later writes were preserved (only the stale run/ lock was reclaimed); the
  previous-build (301a8e9) reader's fsck on the recovered baseline exits 1 on its own build's
  turn-0 MCP rule; the recovery was not activated
```

---

## Where to look next

- [docs/user-guide.md](user-guide.md) — what the commands, slash commands and tools do
- [docs/troubleshooting.md](troubleshooting.md) — what each observation means and what to do
- [docs/cannot-do.md](cannot-do.md) — the capabilities no scenario here can establish
- [docs/config-reference.md](config-reference.md) — every configuration key, generated
- [docs/commands.md](commands.md) and [docs/mcp-tools.md](mcp-tools.md) — the generated surfaces
- [docs/architecture.md](architecture.md) — the contracts behind all of it
- [docs/adr/README.md](adr/README.md) — every architecture decision record, with its status

Packaging and release, added by SP-17:

- [docs/install.md](install.md) — installing, upgrading and uninstalling the bundle
- [docs/security.md](security.md) — the security and recovery posture
- [docs/release.md](release.md) — how a release is cut and what it claims to support

The packaged bundle itself is assembled by `go run ./tools/devtool bundle`; none is committed on this
tree. Every `[requires SP-17 artifact]` step above depends on that assembled bundle and on a host to
install, upgrade or uninstall it against.

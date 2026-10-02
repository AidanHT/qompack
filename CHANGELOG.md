# Changelog

All notable changes to this project are documented here.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project
adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

The release 0.3.0 entry (V6 close-out decision D1). It summarises the user-visible changes since
`v0.2.0`, an internal verification tag that was never released, so this is the first release
published from this repository. Release candidate: release candidate 7, whose commit and frozen
bundles are recorded in `plans/sdd/V6-closeout/phase3/c7-CANDIDATE.md`. At the release this heading
becomes the version and its date (`docs/release.md` §1, step 2).

### Added

- **Checkpoints and rehydration.** `PreCompact` seals an immutable, verified checkpoint of the
  session (with a manifest and a fallback to the previous checkpoint), and checkpoints are also
  sealed on the checkpointer's own cadence. After the host compacts, `SessionStart` with
  `source=compact` injects a rehydration block of at most 9,500 characters, inside the host's
  10,000-character cap: the pinned invariants and the original request verbatim (with the
  corrections made since rendered above it) first, then the session's other records in a fixed
  priority order, whole or not at all, and a drop report naming whatever did not fit together with
  the calls that retrieve it.
- **Retrieval through an MCP server** (`qompack mcp`): `recall`, `expand`, `re_read`,
  `already_tried`, `record_eliminated`, `timeline`, `why` and `dropped`, with minimal spans, paged
  responses bounded by `runtime.mcp.maxResponseBytes`, `path:` globs and the host's own tool names
  in `recall`, and Qompack's own records ranked after the original captures.
- **Slash commands**: `status`, `recall`, `pin`, `why`, `dropped` and `eval`.
- **Negative knowledge**: eliminations recorded with `record_eliminated` and queried with
  `already_tried`, refreshed for staleness when read, and carried into checkpoints.
- **Operator commands**: `qompack status`, `doctor`, `fsck` (five explicit repairs behind
  `--repair --yes`, no destructive default), `self-test`, `config print` and `config schema`,
  `backup create`, `backup verify` and `backup restore` (with the daemon stopped, into a fresh
  destination, proven with the same build's reader and the integrity checks), and
  `admin delivery-seal`.
- **The host's saved Read rules** (`permissions.deny` and `permissions.ask` for `Read`, from the
  managed, user, project and local settings files) are re-checked on every archived retrieval, and an
  unreadable settings file makes path-bearing answers unavailable.
- **Delivery-journal rollover**, on by default: every 65,536 deliveries the journal rotates, each
  rotation is a loud line and a counter, and a project nearing its first rotation is warned once per
  daemon run to take a backup.
- **On Windows the daemon runs from a verified, read-only copy** under `~/.qompack/bin/<sha256>/`, so
  a running daemon no longer holds the plugin directory during an update or uninstall.
- **Packaging and release tooling**: `devtool bundle --archive` builds a reproducible bundle and
  `.zip` for each of the six targets, each with `BUNDLE.json`, `checksums.txt`, `LICENSE` and
  `THIRD_PARTY_NOTICES.md` (which now reproduces the Go runtime's licence); `devtool marketplace`
  generates the six per-target `qompack-<os>-<arch>` marketplace entries pinned by sha256;
  `devtool release-check` is the release gate, `devtool release-scope` reports what the committed
  records establish, and `devtool licenses` keeps the notices in step with the dependency graph.
  A tag's release is drafted as a pre-release, with notes taken from `docs/release-notes/<tag>.md`
  when that file exists.
- **Evaluation**: `devtool live-eval` drives real headless host sessions with and without Qompack
  under a pre-registered protocol, and `/qompack:eval` reports the replay and live results with
  failed trials counted.

### Changed

- **Every hook and the MCP server are exec form**: the host starts the bundled binary directly, so no
  shell parses the command and Windows no longer needs Git Bash. This needs **Claude Code 2.1.139 or
  later**; installing from the marketplace needs 2.1.224 or later. Tested with 2.1.280.
- **Hook output follows Claude Code 2.1.280's schema.** `PreCompact` answers the empty object: the
  summarizer instructions Qompack used to return were rejected by the host, so they are retired.
- **The rehydration fits the host's cap** instead of a token budget of up to about 12,000 tokens,
  which the host would have replaced with a file path and a short preview.
- **A compaction's `SessionStart` answers within 5 s**, and with a "rehydration deferred" note naming
  the cause when the rehydration is late or cannot be built, instead of an empty answer.
- **`session-start` ends inside its 15 s timeout**, and one daemon starts per project however many
  hooks race to start it.
- **The `SessionEnd` flush answers once its request is durable** and the daemon ends the session
  afterwards, inside the host's shared 1.5 s budget for plugin `SessionEnd` hooks.
- **The hot-path budget B-A is derived per platform**: 15 ms on Linux, 50 ms on Windows, 40 ms on
  macOS, so B-B's own durable ingest no longer trips the breach detector systematically on Windows.
  A configured value below the durable-ingest budget is warned about.
- **On a slow disk the switch to spool submode says that nothing is lost**, `status` and `doctor`
  explain it, and a `PreCompact` replays the session's spooled captures, within a 500 ms bound,
  before it seals.
- **Configuration**: a bad key falls back to its default and warns on the hook path too, instead of
  stopping every capture; a `runtime.redact` or `runtime.mode` that cannot be applied as written
  stops recording. A configuration reload says which changed keys need a daemon restart and which
  have no effect in this build.
- **A session whose project root is the home directory records nothing** and says so once.
- **The startup publication pass runs in the background** and yields to capture work.
- **The plugin and marketplace descriptions** no longer claim compaction or cache awareness, and
  `/qompack:pin` describes what a pin does.

### Removed

- **`/qompack:checkpoint`**: it ran the `PreCompact` hook entry point, which wrote nothing and
  reported nothing. Checkpoints are automatic.

### Fixed

Defects of earlier development builds of this repository, found by the V6 verification and its
live sessions:

- **Capture**: events dispatched concurrently were stranded behind the same-session ordering gate
  and never recovered by the drain; client spools are replayed in the order the host stamped them;
  a thrash warning whose reply never reached the host is re-armed.
- **Rehydration**: the first prompt is injected whole or named as overflow, never cut mid-word;
  corrections reach the evolution of the original request and render above it; a forked session
  keeps its parent's request; a pin made while the daemon runs reaches the next checkpoint;
  decisions carry across checkpoints; a checkpoint fallback is named, never silent; checkpoint
  pointers are never empty.
- **Diagnostics**: a healthy session no longer reads as failing in `status`; `doctor` and `fsck`
  agree; Qompack's own MCP records are filed at the current turn, so `fsck` no longer fails after an
  MCP call; a refreshed contract row is dated by its observation.
- **Recovery**: restore works after an idle exit, after store GC of an MCP root and on a store from
  before an upgrade; `backup verify` restores into a scratch copy and runs the seal check.
- **Retrieval**: a cut response always carries `next_span`, an explicit span pages like
  `full: true`, and quarantined or damaged objects answer `unavailable`.

### Security

- On Windows, a Read rule can no longer be bypassed through an 8.3 short name: with a deny or ask rule
  in force, a path with an unresolvable 8.3-shaped segment is refused.
- The cached server-managed settings are read in full, so a deeply nested permissions block is no
  longer dropped.
- Rehydration pointers never show a path the host currently denies, a path outside the project, or a
  home- or variable-rooted path; they point by hash.
- Retrieval resolves a path on disk before answering, so a directory replaced by a link out of the
  project is refused.

### Known limits

- **No claim of benefit.** No document, release note or description claims that Qompack improves
  recovery after a compaction, task success or constraint retention
  (`plans/sdd/V6-closeout/eval/preregistration.md`, amendment A8).
- **Verified where the evidence says, and nowhere else.** Installed into Claude Code on windows/amd64
  only, by the live lanes on candidates 3 and 4. Linux fsync-bound timing rows (B-A, B-B) are not
  verified in target (D53(b)); in the Linux container the whole tree, `test/e2e` and the
  product-child lane pass under `-race`. On Windows, candidate 6's quiet hot-path run passed on AC
  (B-A p99 30.7 ms, B-B p99 24.6 ms against 50), and X11, the hot path with and without a resident
  elimination ledger, passed 3 of 3 rounds on AC (B-A p99 36.9 ms, B-B p99 at most 24.6 ms against
  50, nothing deferred). Runs taken on battery are not reference measurements (D57(d)); on battery
  the hot path switches to spool submode and nothing is lost (D53(c)). Windows reference timings
  were taken on AC with the store under a path excluded from Windows Defender scanning (decisions
  D32, D53(h)). Candidate 6's evidence carries to candidate 7, whose binaries differ from it by one
  unreferenced byte, plus darwin/arm64's ad-hoc signature hash (D57(c)). The executable bit after a
  marketplace install on Linux and macOS, and the command and tool namespace under a
  `qompack-<os>-<arch>` entry, have not been observed.
- **Binaries are not code-signed**, so Gatekeeper, SmartScreen and Defender may refuse or flag them
  (`docs/install.md` §10).
- **Accepted residuals**, each documented in `docs/cannot-do.md`: a 2.3 to 6.8 s capture pause at each
  journal rotation and a GC halt past 65,536 carried leases (D6); the deferred note at the edge of
  session-start's budget (D29); prompts captured out of host order are flagged, never renumbered
  (D35(b), D38); a `SessionEnd` during a daemon stop waits for the next session (D35(c)); spool
  submode lasts until the session or the daemon ends (D44); shorter pages beside redacted text
  (D48); PutBytes and the 256 KB `OnToolUse` row miss their budgets after the hook's ACK (D54); a
  `PreCompact` can wait behind a spool replay already running and then names what it left (D56(e)).
- **`fsck` beside a running daemon** can report an evidence-class retention root that is not held
  while the daemon is still publishing it. Stop the daemon and run `fsck` again; a result taken with
  the daemon running is a snapshot (D57(b), `docs/troubleshooting.md` §9).
- **Not in this build**: a manual checkpoint, `qompack bench`, an operator command that stops the
  daemon, and any automatic downgrade of the store format.

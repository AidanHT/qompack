# Changelog

All notable changes to this project are documented here.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project
adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.3.0] - 2026-10-08

The release 0.3.0 entry (V6 close-out decision D1). It summarises the user-visible changes since
`v0.2.0`, an internal verification tag that was never released, so this is the first release
published from this repository. Release candidate: release candidate 8 (decision D58(e)), commit
`3ec62ad2`, whose frozen bundles and evidence are recorded in
`plans/sdd/V6-closeout/phase3/c8-CANDIDATE.md`; the release tags candidate 8 or a descendant whose
changes reach no bundle.

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
- **Shipped off by default**, present in the binary and best left off: the `runtime.migration` and
  `runtime.phase7` gated switches, output replacement among them (a `true` is refused); SP-21's
  admission gate (explicit opt-in only, with an empty host allowlist) and
  `runtime.selection.submodularEnabled`; and the SP-15 state-aware warning detector. Telemetry is
  hardwired off. The README's "What ships off, and why you should leave it off" and
  `docs/release.md`'s capability table give each one's state.

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
- **The rehydration build's host judgements are bounded.** With a Read deny or ask rule in force, a
  build asks the host about at most 64 path-keyed checkpoint drops, those a summary or a drop reason
  names first, and at most 64 brace alternatives, and treats the rest as withheld; it also reuses
  each screening answer within the build. A long session's rehydration no longer nears the 5 s
  compaction budget, past which the session gets the deferred note instead (D67, D71(d), D72).
- **Section 6 explains a withheld pointer once**, in a legend line under its heading, and the
  withheld lines read `(summary withheld)` or `file (path withheld)`, so the explanation no longer
  crowds pointers out of the block (D67).
- **ToolSearch's `select:` previews are shown** in the block's pointers; they were withheld in every
  project as if `select:` named a PowerShell drive (D67(l)).

### Removed

- **`/qompack:checkpoint`**: it ran the `PreCompact` hook entry point, which wrote nothing and
  reported nothing. Checkpoints are automatic.

### Fixed

Defects of earlier development builds of this repository, found by the V6 verification and its
live sessions:

- **Capture**: events dispatched concurrently were stranded behind the same-session ordering gate
  and never recovered by the drain; client spools are replayed in the order the host stamped them;
  a thrash warning whose reply never reached the host is re-armed; a budgeted drain pass counts only
  progress against its budget, so on a slow host re-reading an already-acknowledged line no longer
  ends the pass, the watcher keeps its backoff, and later client spools are still reached (D58(c));
  a drain pass over blocked spools no longer admits again the lines it already consumed out of
  order (a pass over 4 blocked spools went from 8.2 s to 7.5 ms, and the first pass over a large
  backlog from 546,004 journal queries to 5,107; D62); a redelivered delivery, replayed after a
  daemon stop cut its commit, is applied to the scheduler once, so `state/scheduler.json`'s
  `open_segment_tokens` no longer counts its tokens twice (D62).
- **Rehydration**: the first prompt is injected whole or named as overflow, never cut mid-word;
  corrections reach the evolution of the original request and render above it; a forked session
  keeps its parent's request, and its current work comes from its own prompts, not an inherited
  parent prompt; in every session, current work skips slash-command invocations, never moves
  backwards to an older prompt, and falls back past a newest prompt that cannot be read (D62); a
  pin made while the daemon runs reaches the next checkpoint; decisions carry
  across checkpoints; a checkpoint fallback is named, never silent; checkpoint pointers are never
  empty; a compaction that dropped material is never silent: when the budget admits no section, the
  block is a loss notice naming the loss and the restore route (D59(b)), and below the smallest
  loss notice the drop report and one `LOUD.log` line name it (D60(c)(ii)).
- **Diagnostics**: a healthy session no longer reads as failing in `status`; after a compaction or
  resume in the same session, `status` reads `session_start.fires` as holding
  (`same-session-restart`), not pending (D58(d)); `status --json` lists sessions in one stable order,
  as do the other lists in `status` and `doctor` (D59(c)); `status`, `doctor` and the other
  slash-command frontends that call the daemon have a connect budget of their own (`qompack mcp`
  keeps the hooks' budget and its retry loop, D61(c)), and a connect miss is reported as one, never
  as "did not answer within 10s" (D60(e)); with `runtime.daemon.enabled` false, `status` names a
  disabled daemon instead of reporting a connect miss; `session_start.fires` holds when a session
  compacts or resumes after another session has started (D62); `fsck` lists its detail lines in one
  order, and `eval`'s validation messages read the same on every run (D62); while a newer
  `settingsVersion` is in force, hooks log the reset at `warn` instead of a `LOUD.log` line per hook
  (D59), as they now do for every other configuration violation, and each daemon reports a
  violation in `LOUD.log` once when it starts, not again at its first reload check;
  `state/config-violations.json` is written only when it changes and removed once nothing is in
  force, so `doctor`'s `config.violations` clears when the configuration is fixed and a hook no
  longer pays a durable write on every run while a violation lasts (D62); `doctor` and `fsck` agree;
  Qompack's own MCP records are filed at the current turn, so `fsck` no longer fails after an MCP
  call; a refreshed contract row is dated by its observation.
- **Recovery**: restore works after an idle exit, after store GC of an MCP root and on a store from
  before an upgrade; `backup verify` restores into a scratch copy and runs the seal check.
- **Retrieval**: a cut response always carries `next_span`, an explicit span pages like
  `full: true`, and quarantined or damaged objects answer `unavailable`.

Fixed in release candidate 8's last waves (D67, D68, D71, D72):

- **No false degrade for concurrent or quiet windows**: several windows started at once on a new
  project, a window left quiet past `runtime.daemon.idleExitSeconds`, or a start replayed after its
  own session's `PreCompact` or `SessionEnd` no longer fails `session_start.fires` and drops a
  healthy project to passive recording.
- **No false degrade after a cancelled compaction**: a compaction you cancel (Esc) or that fails,
  followed by a prompt or an exit and then `--resume`, reads `precompact-not-completed` instead of
  failing `session_start.source_compact`, also when the hooks reach the daemon out of order or
  across a daemon restart.
- **A stale `state.bin` no longer strands the hooks**: a `.qompack/run/state.bin` left saying the
  daemon is disabled or the mode is off, by a daemon that died without a clean stop, counts only
  while that daemon still holds its lock or answers; otherwise the configuration decides, so hooks
  start a daemon and record again (D67(c)).
- **A replayed `Stop` no longer shifts turns**: a main-agent `Stop` cut short and replayed is
  recognised, so the turn number of every later record stays where it was.
- **A spooled copy of a sealed `PreCompact` is not sealed again**: it is acknowledged without a
  second checkpoint, a second segment close or a second timing sample.
- **A replayed `SessionStart` no longer re-anchors the scheduler** or takes it from the live session
  it is bound to, and a delivery replayed after a restart, before the scheduler binds, is no longer
  missing from the bound session's token account.
- **A daemon that never idles** (a headless `claude -p` loop, for example) keeps per-session
  scheduler bookkeeping for at most 256 sessions instead of growing for its whole life.
- **Consistent configuration-violation reporting**: `status`, `doctor`, `self-test` and
  `state/config-violations.json` all count a newer-`settingsVersion` reset; a command logs a
  violation once, at `warn`, and only a daemon's start writes it to `LOUD.log`; the daemon's start
  line and a command's line name the file, variable or flag that set it (`location=`); no command
  creates `.qompack/` just to record a configuration violation in a directory that has none; a
  hook under `runtime.mode` `off` writes no configuration diagnostics; and `doctor` reads the record
  without following a link or hanging on a FIFO.
- **Clearer status reasons**: `doctor` no longer says it asked a daemon to start (it never starts
  one), and with `runtime.mode` `off`, `status` says the mode is off instead of "decoding status:
  unexpected end of JSON input".
- **Deterministic MCP reasons**: when one object comes from several refused paths, `recall`,
  `expand`, `why`, `re_read` and `dropped` give the same withheld reason on every read.
- **A drop reason keeps Qompack's own commands**: a reason that says to run `/qompack:pin --list` is
  no longer withheld as if `/qompack:pin` were a path outside the project.
- **A compaction no longer re-reads a large pasted prompt** to open the next checkpoint: the new
  draft takes the current work from the one just sealed.
- **Decisions across checkpoints**: a forked session's checkpoint carries its parent's decisions as
  of the fork point, and a decision a sealed checkpoint truncated away is carried again as it was
  made, alternatives included (D67(a)).
- **No false "publication accounting incomplete"**: a healthy store's background publication pass no
  longer logs that `LOUD.log` line when a file is removed or replaced while the pass reads it.

### Security

- On Windows, a Read rule can no longer be bypassed through an 8.3 short name: with a deny or ask rule
  in force, a path with an unresolvable 8.3-shaped segment is refused.
- The cached server-managed settings are read in full, so a deeply nested permissions block is no
  longer dropped.
- The rehydration block's pointers do not show a path the host's saved Read rules deny or an
  absolute path outside the project (D50). A file pointer is judged whole, as `re_read` judges a
  path, and points by hash; its home- or variable-rooted path is withheld too. A structured
  tool-argument summary (the store's preview of a path argument) is judged whole the same way and,
  when refused, is replaced by a "summary withheld" note; a path-named value holding several paths
  is judged piece by piece. A free-text summary (a command line, a search query) is shown only when
  a whitelist proves it safe (D63, tightened by D64): every whitespace-delimited token is built from
  letters, marks, digits and a small set of safe punctuation (a few shell operators, a simple quoted
  run, an http(s) URL), no token names an absolute path or one that escapes the project, and no Read
  deny or ask rule's literal and no withheld path's name stands where a name starts. The string
  values of a JSON preview are each judged that way, and a one-word summary must pass too. Anything
  else is withheld, and so is every free-text summary while the host's rules cannot be read. The
  whitelist over-withholds by design (D64(4)): a command that uses a variable (`echo $HOME`), a glob
  (`find . -name "*.go"`), a regular expression, a `%` escape or a name and a `:` where a path may
  start (`curl localhost:3000`, `git log --pretty=format:%h`) is withheld whether or not it names a
  denied file. The project root is held together as one root unit, so `cd <root> && go test` is
  shown, only when its spelling is plain: letters, marks, digits, `-`, `_`, `.`, its separators and
  single spaces, with no word starting with `-` (D64(1)); under any other root a summary that spells
  the root is withheld. Section 7's drop entries never show such a path (D60(c)). The documented
  limits are under Known limits below and in `docs/cannot-do.md` §5.
- Inside a path-named tool argument, a piece glued to the one before it by a control character, a
  quote, a backtick, a non-ASCII space or another non-ASCII character that is not a letter is judged
  as a piece of its own (an ASCII `+ # ) ] } ! ^` is not; see Known limits below), and so is each
  alternative of a `{a,b}` brace list; a name a shell builds at run time (`$(…)`, `${…}`, a
  backtick, a cmd `%VAR%` or `!VAR!` after a run of dots, a batch parameter, a shell tilde such as
  `~+` or `~$USER`) reads as outside the project. So the rehydration block no longer shows `.env`
  from `src/a.ts` and `.env` glued by a NUL under `Read(./.env)`, or
  `~{,x}/.ssh/id_rsa`; a free-text JSON string holding a NUL or DEL is judged with it read as a space
  (D71, D72). Rarer spellings remain; see Known issues below and `docs/security.md` §1.
- Retrieval resolves a path on disk before answering, so a directory replaced by a link out of the
  project is refused.

### Known limits

- **No claim of benefit.** No document, release note or description claims that Qompack improves
  recovery after a compaction, task success or constraint retention
  (`plans/sdd/V6-closeout/eval/preregistration.md`, amendment A8).
- **Verified where the evidence says, and nowhere else.** Loaded into Claude Code on windows/amd64
  only: candidate 8's frozen bundle by its live re-check, 20 real sessions in which every scenario
  passed (D76), and by its live evaluation (D77), and earlier candidates' bundles by the live lanes
  on candidates 3, 4 and 7. Linux fsync-bound timing rows (B-A, B-B) are not verified in target
  (D53(b)); in the Linux container the whole tree, `test/e2e` and the product-child lane pass under
  `-race`. On Windows, candidate 8's quiet hot-path run passed on AC (B-A p99 16.4 ms, B-B p99
  11.3 ms against 50), and X11, the hot path with and without a resident elimination ledger, passed
  on AC (B-A p99 26.6 ms without the ledger and 20.5 ms with it, B-B p99 12.3 ms, against 50). Runs
  taken on battery are not reference measurements (D57(d)); on battery the hot path switches to
  spool submode and nothing is lost (D53(c)). Windows reference timings were taken on AC with the
  store under a path excluded from Windows Defender scanning (decisions D32, D53(h)). Hosted
  `ci.yml` run `37562946379` and `nightly.yml` run `37562945914` passed on candidate 8, and the
  hosted release-version bundles were byte-identical to its frozen ones (D75). The live evaluation's
  pre-registered decision reads "inconclusive — interval [-0.214, 0.214] straddles -0.200": at this
  sample size that is the expected verdict, by design, and it is not evidence that Qompack adds
  nothing (amendment A8, item 3). The C5.2 benchmark night and the C1.16 re-measure are in
  `docs/release.md`, release status (D62(b), D62(c), D65). The executable bit after a marketplace
  install on Linux and macOS has not been observed, nor has an install from the published
  marketplace. Under an entry named `qompack-windows-amd64`, installed from a local marketplace on
  candidate 7, a session listed the server `plugin:qompack:qompack`, the tools
  `mcp__plugin_qompack_qompack__<tool>` and the commands `/qompack:<name>`: the namespace comes from
  `plugin.json`'s name, not from the entry's (D59; `docs/install.md` §9).
- **After a daemon is killed mid-session**, the daemon that takes the project over can reach its
  idle exit without writing `index/files.json`, so a later `fsck` exits 1 naming `index.files`
  absent. Nothing is lost: `fsck --repair --yes` regenerates the view, and the next session's flush
  writes it (D59, `docs/troubleshooting.md` §9).
- **When the original request overflows the rehydration block**, older evolution entries are not
  re-admitted into the room the block leaves unused: authority order comes first (ADR 0011). They
  are named in section 7, and `dropped()` lists them (D59, `docs/troubleshooting.md` §5).
- **Below the smallest loss notice, nothing is injected.** A rehydration budget too small for even
  "N items dropped; call dropped()" gets no block; the drop report records the overflow and
  `LOUD.log` gets one line, so the loss is named, never silent (D59(b), D60(c)(ii),
  `docs/cannot-do.md` §4).
- **The rehydration's free-text whitelist** (D63, D64) withholds more than it must: variables,
  globs, regular expressions, `%` escapes and `name:` shapes such as `localhost:3000` and
  `format:%h` are withheld even when they name no denied file (D64(4), D67(l)), and under a root
  whose spelling is not plain, no summary that spells the root is shown, because the root unit
  applies only to a plain root (D64(1)). It does not resolve aliases (8.3 names, links, Unicode
  variants of a name) or see names built at run time or relative to a `cd`, and the store's preview
  collapses runs of whitespace, so a summary is judged as collapsed, not as the command spelled it
  (D60(c)(iv)). Globs in free text are always withheld; a structured glob (a lone Glob or recall
  pattern) that selects a refused file the block never recorded, without spelling its literal
  (`private/d*`), is judged as written (D60(c)(iv)). Inside one path-named value, a rooted path
  glued after one of `+ # ) ] } ! ^` (`a.txt+\Windows\win.ini`) is not judged as a path, so it can
  be shown; the same text in free text is withheld (ADR 0011 §23, `docs/security.md` §1). The
  records in sections 2 to 4, your own prompts and the model's own earlier text, are outside D50
  (D60(c)(i) for sections 3 and 4, D62(f) for section 2; `docs/cannot-do.md` §5).
- **On macOS, Qompack assumes the default case-insensitive volume**: it compares paths and the Read
  rules' patterns without regard to letter case. On a case-sensitive APFS volume, two names that
  differ only in case are two files, which Qompack reads as one (D67(m), `docs/cannot-do.md` §5).
- **The recorded-corpus tier of the replay evaluation is not exercised in 0.3.0.** No recorded
  corpus is committed and no test reads real transcripts; the replay evidence is the replay gate's
  (D67(g)).
- **Backup refuses while a newer `settingsVersion` is in force**, after a plugin downgrade: take the
  backup with the newer build first (D59, `docs/backup.md`, `docs/troubleshooting.md` §6).
- **Binaries are not code-signed**, so Gatekeeper, SmartScreen and Defender may refuse or flag them
  (`docs/install.md` §10).
- **Accepted residuals**, each documented in `docs/cannot-do.md`: a 2.3 to 6.8 s capture pause at each
  journal rotation and a GC halt past 65,536 carried leases (D6); the deferred note at the edge of
  session-start's budget (D29); prompts captured out of host order are flagged, never renumbered
  (D35(b), D38); a `SessionEnd` during a daemon stop waits for the next session (D35(c)); spool
  submode lasts until the session or the daemon ends (D44); shorter pages beside redacted text
  (D48); PutBytes and the 256 KB `OnToolUse` row miss their budgets after the hook's ACK (D54); a
  `PreCompact` can wait behind a spool replay already running and then names what it left (D56(e));
  a quiet live session (a long reply with no tool call, a long compaction) is counted as ended until
  its next hook, which revives it, and nothing captured is lost (D62); a command whose connect to a
  running daemon misses its budget can start a second daemon, which finds the lock and exits, with
  nothing lost (D61(c), `docs/troubleshooting.md` §1); and in a project with two live sessions,
  another session's tool use counts toward the scheduler's bound session and can close its segment
  (D67(b)).
- **`fsck` beside a running daemon** can report an evidence-class retention root that is not held
  while the daemon is still publishing it. Stop the daemon and run `fsck` again; a result taken with
  the daemon running is a snapshot (D57(b), `docs/troubleshooting.md` §9).
- **Not in this build**: a manual checkpoint, `qompack bench`, an operator command that stops the
  daemon, and any automatic downgrade of the store format.

### Known issues

Minor defects this release does not fix, each recorded in the close-out ledger (D66(d), D67(o)).

- **Claude Code windows left quiet across two idle exits.** If windows stay open with no hook past
  `runtime.daemon.idleExitSeconds` (30 minutes by default) through two daemon idle exits, and a new
  window is started after each, the second start fails `session_start.fires` and the project drops
  to passive recording. It cannot happen once any session of the project has ended or compacted; to
  recover, exit one session normally, and full recording returns after two later session starts
  pass their checks (`docs/troubleshooting.md` §1 and §2).
- **A fork's decisions on a score tie.** When the rehydration budget runs short and scores tie, a
  forked session's block can keep a decision inherited from its parent ahead of the fork's own. The
  cut decision is still named in `dropped()`, and `why()` retrieves it.
- **Path-keyed drops past the judgement bound.** In a project with a Read deny or ask rule, a
  rehydration build asks the host about at most 64 path-keyed checkpoint drops, those a summary or a
  drop reason names first. Later ones show as "(path withheld)" in section 7 and in `dropped()`, and
  a section 6 summary that names one may be withheld too: more is hidden than must be, never a
  refused path (D71(d)).
- **Rehydration build cost.** A build with no checkpoint drops costs about 1.7 times candidate 7's,
  about 2 ms more, because of the summary whitelist (D63). That is far inside the 5 s compaction
  budget.
- **Hook warnings name no location.** A hook's "invalid configuration value" line in the day log does
  not say which file, variable or flag set the value, and `qompack config print --provenance` shows
  that key only as `fallback after violation`. To find it, run `qompack status`: its `warn` line in
  the day log names the source under `location=`, as the daemon's start line does
  (`docs/troubleshooting.md` §6).
- **One log line under `runtime.mode` `off`.** When a hook's own read of its delivery fails, it still
  appends one line naming the read error to `.qompack/logs/hook-quiet-YYYYMMDD.jsonl` where
  `.qompack/logs/` exists, until `.qompack/run/state.bin` also says off. To have the hook path write
  nothing at all, remove the plugin (`docs/troubleshooting.md` §8, Steps 3 and 5).
- **A large paste read once after a restart.** After a daemon restart, the first compaction of a
  session whose newest prompt is a very large paste reads that prompt once in full.
- **A slow `PreCompact` can be sealed twice.** If a `PreCompact`'s reply misses the hook's 15 s
  deadline and the hook's spooled copy is replayed before the slow seal finishes, that compaction
  gets two checkpoints. The copy is never dropped before a seal succeeds, so the compaction always
  keeps one.
- **A replayed `SessionStart` marks its session live.** A `SessionStart` replayed from a hook's
  spool, for a session that had ended or that this daemon never saw, marks that session live: a
  later replayed delivery of it can bind an unbound scheduler to it until the live session's next
  start, and the daemon's idle exit waits for that session's silence timeout.
- **A lost segment close.** A segment close owed by a delivery whose first run was cut is held only
  in memory. If that run, its replay at `Stop` and its replay after a restart are all cut, the close
  is never made and the span stays in the next segment, at a coarser boundary; nothing captured is
  lost (D67(b)).
- **Five more are listed under Known limits above**: the single scheduler account per project,
  which lets another session's tool use close the bound session's segment (D67(b)); the second
  daemon a missed connect to a busy daemon can start, which loses the lock and exits (D61(c)); the
  recorded-corpus tier of the replay evaluation, not exercised (D67(g)); macOS's assumed
  case-insensitive volume (D67(m)); and a rooted path glued after one of `+ # ) ] } ! ^` inside one
  path-named value, which can be shown (ADR 0011 §23).
- **Unusual spellings of a path inside a tool argument.** The rehydration block judges each path in
  a path-named tool argument, but a few rare spellings can still show a path outside the project or
  one a Read rule refuses: a cut-off `~[name`; a home directory named by a login that holds `@`, `$`
  or a non-ASCII letter; a Windows `%VAR%` whose name is not an identifier; look-alike Unicode
  slashes or dots (`／`, `∖`, `．`); and a non-canonical spelling (`a/./b`) of a refused path whose
  file name is shorter than 3 bytes (D72(a)). They are the first fix planned after this release.
- **`/qompack:status` lists a "last decision" it does not show.** The command's description, and
  `docs/commands.md` generated from it, say status reports the last decision, but neither the status
  page nor `qompack status --json` carries one. To see a recorded decision, run
  `/qompack:why <decision-id>` (D76(b)).
- **A session end deletes an unindexed object before it can be audited.** After a crash that left an
  object written but not indexed, the next session end's garbage collection deletes that object, the
  one the daemon's startup accounting reported. No capture content is lost: the capture keeps its
  bytes inline in its sidecar, and `qompack fsck` still names the gap. To keep the object itself,
  copy the store before the next session ends (`docs/backup.md`, D76(c)).

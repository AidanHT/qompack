# 15. Integrating with Claude Code mods: a thin in-process mod over the Go engine

Date: 2026-10-14

## Status

Accepted for Stage A: the wire contract, the mod's source layout and the build rules below are in
force from the commit that adds this record. Values marked **provisional** are build-time defaults
that live checks against a pinned Claude Code build confirm or replace; no live Claude Code session
was run to write this record. After this record lands, a change to the wire contract
(`internal/modwire`) needs an explicit maintainer ruling, because the mod and the binary ship
together but are written by different hands.

## Context

Claude Code 2.1.287 and later load *mods*: plugins whose `hooks/hooks.json` names a TypeScript or
JavaScript hooks module that runs inside the host process. A mod's hooks can see and steer events
that Qompack's classic exec hooks cannot, compaction among them (`session.compact`, whose input
carries the compaction's `instructions` and whose hook may rewrite them before the summary runs).

Qompack is a Go engine: a binary that the classic hooks start, a resident daemon, and a store under
the project's `.qompack/`. The question this record settles is how a mod and that engine divide the
work, and what the boundary between them looks like.

### Sources

Every host fact below was read from one of these, fetched 2026-10-14. The host's own documentation
says the type declarations a build writes for itself outrank every page, so each fact taken from a
type declaration or a page is provisional until it is checked against the pinned build's own types.

- Claude Code mods documentation: overview, create, reference, api, events, test and troubleshoot
  pages under `https://code.claude.com/docs/en/plugins/mods/`, and the Claude Code changelog.
- The published mods type declarations, `mods/types/claude-code.d.ts` in the
  `anthropics/claude-code` repository. The copy read says on its first line that Claude Code
  2.1.277 wrote it, which is older than the release that turned mods on by default; it is
  therefore read as provisional.
- A probe mod (classic hooks plus a hooks module, with tests) validated and tested on the
  maintainer's machine against Claude Code 2.1.293 without a model call, and `claude plugin
  validate` / `claude plugin test` runs of this repository's mod sources against the installed
  2.1.300 on a scratch copy outside the repository.
- This repository: `internal/pluginmanifest` (the bundle generator and `BinaryRef`), `internal/ipc`
  (the IPC client and its disk spool), `internal/cli` (the hook client's project-root resolution,
  home-root refusal and connect-deadline floor), `internal/config` (connect deadlines and the
  daemon's idle exit), and `tools/devtool` (the `gen-*` tasks and the format and marker gates).

## Decisions

### 1. The Go engine stays the single source of truth; the mod is thin

The mod keeps no durable state and does not use the host's `$.store`, which lives under the user's
Claude configuration directory rather than in the project. Everything Qompack records stays in the
project's `.qompack/`, under the same write-set rules the classic hooks obey. The mod's only job is
to carry a host event across to the binary and carry the answer back.

### 2. The bridge is `qompack mod <verb>` over the host's process API

The mod runs the bundled binary with `$.process.run(argv, { cwd, stdin, timeoutMs })`, which the
type declarations describe as resolving `{ exitCode, stdout, stderr }` whatever the exit code and
rejecting when the program cannot start or outlives its timeout, with no shell.

- **The binary path.** `$.process.run` expands no `${CLAUDE_PLUGIN_ROOT}`, the placeholder the
  classic hooks rely on (`pluginmanifest.BinaryRef`). `pluginmanifest.BinaryRelPath(goos)` gives
  the same executable relative to the plugin root (`bin/qompack`, or `bin/qompack.exe` on Windows),
  sharing its name constant with `BinaryRef`. The bundle generator writes it into the mod's
  `target.gen.ts`, and the mod joins it onto `$.plugin.root` (documented as the plugin's absolute
  directory), using the root's own path separator.
- **The argument vector** is `[binary, "mod", verb]`; one JSON object goes on stdin and one comes
  back on stdout.
- **The project root.** The mod never decides it. It sends `cwd: await $.session.root()` and uses
  the same directory as the child's `cwd`; the Go side resolves the project root from it exactly
  as the classic hooks do (`QOMPACK_PROJECT_ROOT`, else the nearest `.git` above, else the
  directory itself) and refuses a home-directory root the way they refuse it. The child inherits the
  host's environment, so the override reaches both paths alike. The mod sets no environment
  variables and does not rely on `CLAUDE_PLUGIN_ROOT` in the child's environment, which no source
  documents.
- **No spool.** `ipc.Client.Send` spools a request to disk on any failure for later replay. A brief
  replayed after its compaction has run is worse than none, so `mod` verbs build their client with
  the no-op spool `qompack mcp` already uses.
- **Connect deadline.** `mod` verbs dial with at least the 250 ms floor the hook client gives every
  operation off the hot path; the configured 5 ms (25 ms on Windows) default is sized for a warm
  hot-path connection and would report a daemon that is merely between accepts as `busy`.

### 3. The wire contract (`internal/modwire`)

- Every request and response carries `"v": 1`. A reader ignores fields it does not know. Keys
  match as `encoding/json` matches them: case-insensitively, a repeated key resolving to its last
  value. A message whose `v` is absent or different is refused with an error object, never guessed
  at.
- Stage A has two verbs. `hello` carries `{v, session_id, cwd, host_version}` and answers
  `{v, ok, reason?, detail?, enabled, steering}`; `steering` is true only on a served hello that
  is `enabled` while `runtime.mod.steering` is on (decision 5). `brief` carries
  `{v, session_id, cwd, host_version, trigger, user_instructions_present, max_chars}` and answers
  `{v, ok, reason?, detail?, instructions, fact_ids}`; `host_version` is the `version` field of
  `$.session.version()`, the same value the hello sent. Only the *fact* that the person gave their
  own compaction instructions crosses the boundary, never the text. The `fact_ids` are returned to
  the mod with the instructions; no Stage A path records them anywhere else, and a later stage
  may. Later verbs are additions under the same rules.
- **Triggers.** `manual`, `auto` and `plugin` (`modwire.TriggerManual`, `TriggerAuto`,
  `TriggerPlugin`) make a brief close the session's open segment before it reads the store
  (`modwire.ClosesSegment`). `precompute` (`TriggerPrecompute`), a summary the host computes ahead
  of a compaction while the conversation goes on, and any other well-formed trigger leave the
  segment open, with no segment side effect, and are still served. The cost of that conservative
  default is that a trigger the host adds later briefs from closed segments only. A malformed
  trigger is refused as before. The four names are published to the mod's TypeScript.
- **The brief marker.** Served, non-empty `instructions` begin with `modwire.BriefMarker`, a fixed
  ASCII heading line ("Qompack - reproduce these items verbatim:") that the summarizer reads as an
  ordinary heading. Go's `BriefResponse.Validate` and the mod's decoder both refuse served
  instructions that do not begin with it, as `error`. The mod's `hasBriefMarker` reports whether a
  compaction's instructions already contain it, so the mod can skip a second brief for a
  compaction it already steered. The marker counts toward the instructions' cap.
- An unserved verb answers `ok: false` with exactly one reason: `cold` (no daemon listening; the
  CLI starts the detached daemon and returns at once without waiting for it), `busy` (a daemon
  exists but did not accept in time), `timeout` (it accepted but missed its deadline), `refused`
  or `error` (anything else, including a malformed or unsupported request). Every verb is
  `refused` for a home-directory root, a project with no `.qompack` store, `runtime.mod.enabled`
  false, or `runtime.mode` off as the hooks read it; a brief is also `refused` when
  `runtime.mod.steering` is false or its `host_version` is below `MinHostVersion` or unreadable.
  Nothing is written or spawned for a refused verb, so no daemon, store, hello file or brief
  record. The switches are checked before the mode, because they are off by default and reading
  the mode can probe a daemon. `refused` is the only encoding of "switched off"; a hello from a
  host that is merely too old is answered differently (decision 5).
- A failure's `detail` names the problem, never content from the request, and never an absolute
  path: the CLI writes the project root as `.` and the home directory as `~`.
- The CLI exits 0 with `{ok: false, reason}` for every failure it handles. The mod reads a non-zero
  exit, unparsable stdout, or an answer that breaks the contract as `reason: "error"`.
- **Limits are named constants**: `BriefMaxChars` (4,000) is both the default and the ceiling for a
  brief's instructions; `MaxMessageBytes` (64 KiB) bounds every message in both directions, so
  neither side depends on how the host truncates a long stdout, for which no source states a
  limit; session ids, host versions, triggers, fact ids and the free-text `detail` have byte
  bounds. Fact ids are restricted to the ASCII alphabet `[A-Za-z0-9._:/-]`, every byte of which
  encodes as itself in JSON (with `encoding/json`'s HTML escaping on, which is why `<`, `>` and `&`
  are excluded), so their wire size is their length and the Go side and the mod agree on every id.
  The stored facts' own ids fit unchanged: decision ids (`dec_…`), pinned-invariant ids (`inv_…`),
  negative-knowledge record ids (`elim_…`), content hashes (`sha256:…`) and host tool-use ids. A
  test builds a response at every bound at once from the costliest allowed bytes (after the brief
  marker) and proves it still fits one message.
- **Characters are UTF-16 code units**, what the mod's `String.prototype.length` reports, so the Go
  side (`modwire.CharCount`) and the mod agree on whether a text fits.
- Session ids name per-session files, so they are restricted to `[A-Za-z0-9._-]`, at most 128
  bytes, and never `.` or `..`. A trigger is any plain identifier, `[A-Za-z0-9_-]` up to 32 bytes,
  so a trigger the host adds later is served and recorded; only a malformed one is refused. The
  three alphabets are published as constants, and both sides build their checks from them.
- `internal/modwire` also defines the two records the Go side writes for a mod session, so the
  writer and every reader share one type. Each carries `"v": 1` like a message, and a record whose
  `v` is absent or different is refused on read. The **brief-outcome record** is one JSONL line per
  brief call in `.qompack/state/mod-brief-<session>.jsonl`, with the closed field set
  `{v, ts, trigger, outcome, chars, ms}`, where `outcome` is `served` or the reason, and no
  content. A refused call gets no record, and no store is created for it. The **hello file**
  `.qompack/state/mod-hello-<session>.json` holds `{v, session_id, host_version, ts}`, the proof a
  mod session reached the binary; it is written for every served hello, including one answered
  `enabled: false`, and never for a refused one. The `state` directory is the store layout's own
  (`paths.Layout.State`); a test pins `modwire.StateDir` to it.
- The TypeScript half of the contract, `internal/pluginmanifest/modsrc/wire.gen.ts`, is generated
  from `modwire.Schema()` by `go run ./tools/devtool gen-mod-wire`; its `--check` mode runs in the
  CI `docs` job, and a test holds `Schema()` to every exported declaration of the package.

### 4. Fail open

A slow or failing `qompack mod` call never stops a compaction. The mod's hook catches its own
errors and passes the event on unchanged; the documentation says a hook that throws before calling
`next` is skipped by the host. On a surface without `$.process` (the type declarations mark it CLI
only, while mods also run in the desktop app) the same path applies and no hello file is written.
The classic hooks keep running exactly as they do without a mod.

### 5. Versions and switches

The mod's features are enabled only on a host at least `modwire.MinHostVersion`, **provisionally
2.1.300**, the release installed when the contract was built; a live check of the pinned build
confirms or raises it. The Go side is the only gate: `hello` and every `brief` report
`$.session.version()`, and the CLI applies `modwire.MeetsMinHostVersion` to each. A version must
begin `MAJOR.MINOR.PATCH`; a `+build` suffix does not order, a prerelease of a release
(`2.1.300-dev…`) counts as below that release, and anything unreadable does not meet the minimum.
A host below the minimum, or with an unreadable version, gets a *served* hello with
`enabled: false`, and the hello file is still written so the host version is on record for
diagnosis; a brief from such a host is `refused`. The `MIN_HOST_VERSION` constant in the mod's
generated TypeScript is informational.

Two configuration switches keep the features off by default until an evaluation supports turning
them on. `runtime.mod.enabled` lets the mod reach Qompack at all: while it is off, or while
`runtime.mode` is off, every verb answers `refused`. `runtime.mod.steering` lets a brief steer a
compaction: a served, enabled hello reports it as `steering`, and while it is off every brief is
`refused` and nothing is written for it. An explicit field is preferred to overloading `enabled`
because Stage B adds further features with switches of their own. The mod calls `brief` only
after a hello answered `steering: true`. Hello fires once per mod load, so the mod caches the
answer for the process and says hello again before the first brief when no served answer is
cached. Mods can also be off for reasons Qompack cannot see (the host's `disableAllHooks`, safe
mode, bare mode, managed policy, or a remote rollout switch); in each case the classic path runs
as today.

### 6. Provisional timing defaults

The mod waits `modwire.TimeoutMs` (**provisionally 2,000 ms**) for a child, and the daemon answers
within `TimeoutMs - DaemonDeadlineSlackMs` (500 ms) so the CLI can still report a timeout before the
mod gives up. Time the hook spends inside `$.process.run` does not count against the host's hook
time limit. A measurement on a warm daemon with a realistic backlog replaces both values.

### 7. Where the mod's sources live and how they are checked

- The sources live in `internal/pluginmanifest/modsrc/` and are embedded with
  `//go:embed modsrc/*.ts`, the repository's first `go:embed` and first product `.ts`. The pattern
  takes top-level files only, so `modsrc/tests/` is never shipped; `pluginmanifest.ModSources(goos)`
  returns the set with each target's own `target.gen.ts`.
- `register.ts` holds every call on the host's `$`. The host's validator documents that `$` may be
  passed only to a top-level function of the same file, and that passing it to a function imported
  from another file fails validation; the 2.1.299 changelog describes a fix for source-loaded mods
  refused in that case. The two sources disagree, so every call stays in `register.ts`, which both
  allow. `compact.ts` and `format.ts` are pure and never take `$`. Go tests enforce this, the
  validator's import rule (relative imports of the mod's own files, plus `claude-code`, which only
  `register.ts` may import, so no other source can name the host's types), and the rule that
  `register.ts` passes `$` only to a function it declares at its top level, never to an imported
  helper that could take it under another name.
- The tests import the sources as `../hooks/<file>.ts`, the layout of the materialised plugin, where
  the sources sit beside `hooks/hooks.json` (the module path is relative to that file) and the
  tests in `tests/`. The mod's decoder tests carry every Go response golden verbatim, and a Go test
  fails when a golden changes and they do not.
- Three gates get explicit rules for `.ts` under `modsrc/` only, so nothing that passed before
  changes: `devtool fmt` repairs and `fmt-check` enforces LF, no byte-order mark, no trailing
  whitespace and one final newline; the `docmarkers` lint scans their comments for unfilled
  placeholders; `.gitattributes` pins them to `text eol=lf`. The `.ts` fixtures under `testdata/`
  elsewhere, whose CRLF and trailing whitespace are deliberate, are untouched.
- The committed `plugin/` tree does not carry the mod. Whether a bundle includes it is a generator
  option that stays off until an evaluation supports turning it on.

### 8. Privacy and trust

The Stage A mod calls only `$.process.run` (its own binary), `$.session.id`, `$.session.root`,
`$.session.version`, `$.plugin.root` and `$.ui.log`. Brief content comes from the redacted store and
obeys the host's Read deny rules exactly as rehydration does. Mods run unsandboxed in the host
process, which the user documentation must say plainly.

## Consequences

- The engine, its store and its tests stay in Go; the mod is a small, mostly pure TypeScript layer
  whose host calls are listed in one file.
- Every host fact this design leans on is provisional until checked against the pinned build's own
  type declarations; where a check is still open, the code uses a named constant, so a correction
  is a one-line change in `internal/modwire` or `internal/pluginmanifest`.
- A wire change is a two-sided change by construction: the Go types, their goldens, the generated
  TypeScript and the mod's decoder tests move together or CI fails.

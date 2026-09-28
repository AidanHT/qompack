# Troubleshooting

This page is about what Qompack *shows* you when something is wrong, and what each of those
observations does and does not license you to conclude.

Every entry has the same shape: **Symptom** (what you see), **Diagnose** (the command and the field
to read), **Meaning**, **Action**. Where an entry quotes output, the quoted text was produced by a
binary built from this tree and run in a throwaway directory outside the repository; where it
quotes behaviour it could not observe, it names the source file it was read from, or says it is
unknown and what would settle it.

What the commands, the slash commands and the MCP tools *are* is
[docs/user-guide.md](user-guide.md); this page does not repeat it.

> **These diagnostics write.** Under a default configuration, `qompack status`, `qompack self-test`
> and every hook entry point create `<project>/.qompack/` in the project directory they resolve, and
> start that project's daemon. Running one in a directory that has never been used with Qompack is a
> write, in that directory. (§6 and §8 cover the configurations under which a hook writes nothing.)
> `qompack config print` does not create the layout
> ([docs/user-guide.md](user-guide.md#operator-commands)), but it does write
> `.qompack/state/config-violations.json` into one that already exists (observed on this tree). The
> full write set is [docs/architecture.md §2](architecture.md#2-write-set-and-retention). The one
> exception is a project root that is your home directory: there none of them writes anything
> ([below](#qompack-is-inactive-in-the-home-directory)).

## 1. Start with provenance

Do not start from a symptom. Start from what this installation can currently observe, because most
"Qompack is broken" reports are a gap in observation, and the gap is reported honestly.

### `qompack self-test`

**Symptom.** You want to know whether anything is wrong at all.

**Diagnose.** Run it and read three columns: `OK`, `SEVERITY`, `OBSERVED`. `--json` gives the same
rows as `{checks,mode,exit}`. A critical failed check returns exit 1
(`internal/cli/selftest.go`, `runSelfTest`). `fsck` also returns exit 1 for a defect
or an incomplete check. `doctor` reports capability and diagnostic states in its
output; a successful command exit alone does not certify those capabilities.
`bench` remains unimplemented and returns exit 1 (§9).

The first eight rows are this build checking itself: `config.load`, `config.capture`,
`qompack.writable`, `paths.guard`, `ipc.resolve`, `daemon.reachable`, `admin.ping`, `ops.coverage`.
The rest are the
nine host-contract assertions of `internal/contract/ids.go`: `session_start.fires`,
`session_start.source_compact`, `hook.additional_context_delivered`,
`precompact.has_time_to_write`, `precompact.custom_instructions_accepted`, `hook.payload_shape`,
`mcp.server_registered`, `transcript.readable` and `plugin.root_resolves`.

Where the project root is your home directory none of those rows runs, because each would lay out,
probe, lock or spawn for a directory Qompack refuses. The report then has one row, `project.root`:
critical, with the directory and the fix in `OBSERVED`, `mode: off`, exit 1
([the home directory](#qompack-is-inactive-in-the-home-directory)).

`config.load` and `config.capture` answer different questions. `config.load` asks the soft loader
every read command uses, which falls back instead of failing, so it passes for almost any file.
`config.capture` asks the loader every *hook* uses, `config.LoadForCapture`, and changes nothing
(`internal/cli/selftest.go`, `selfTestCaptureConfig`). It is `ok` only when the hooks apply your
configuration exactly as written; `warn` when they apply it with a fallback or a dropped key —
recording continues, and `DETAIL` names every key; and a **critical** failure, exit 1, when they
refuse it, in which case every hook admits nothing (§6). A `config.load ok` beside a failed
`config.capture` is not a contradiction: it is the case §6 describes.

**Meaning — and the one trap on this page.** `ok` in the second column does not mean "verified". In
an empty directory on this tree, `self-test` reported `mode: full`, exit 0, and these rows among
others:

```
hook.additional_context_delivered ok    info      not-yet-implemented
precompact.has_time_to_write     ok    info      not-yet-implemented
precompact.custom_instructions_accepted ok    info      not-yet-implemented
mcp.server_registered            ok    info      not-yet-implemented
session_start.fires              ok    info      first-session
transcript.readable              ok    info      no-transcript-path
plugin.root_resolves             ok    info      unset
```

`not-yet-implemented` is the state `internal/contract/assertions.go`'s `gated` wrapper reports for
an assertion whose producer has not been declared in this build: the real check "never runs at all".
The other three are in a table this repository maintains for exactly this purpose —
`internal/contract/observation.go`'s `noObservationSpellings`, "every Observed string in
assertions.go that means 'nothing was seen', as opposed to 'something was seen' or 'the contract was
broken'". `first-session`, `no-precompact-pending`, `no-transcript-path` and `unset` are all in it,
alongside `no-samples`, `not-yet-observed` and others you may meet — including `retired`, which is
what `precompact.custom_instructions_accepted` reports wherever a daemon has declared its producer:
the PreCompact instruction it once probed for is retired, because no host accepts one (C1.18,
[docs/cannot-do.md](cannot-do.md#no-summarizer-model-substitution)). **In every one of those cases
`ok` means "no assertion was made", not "the host contract holds."** Only a row whose `OBSERVED`
column describes a real observation is evidence.

That table is the authority, not this page: it is pinned by a test that parses `assertions.go` and
fails the build if a check grows a spelling the table does not classify, so a new "nothing was seen"
word cannot quietly start reading as success.

**Action.** Read `OBSERVED`, never the `OK` column alone. To learn what a given assertion would
observe if it ran, read its constant's comment in `internal/contract/ids.go`.

### `qompack status`

**Symptom.** You want to know where the numbers on this page came from.

**Diagnose.** The first line is the provenance of everything below it — on this tree, in a scratch
project with a daemon running: `source: daemon (available, 0µs old)`. It is followed by the
host-contract banner, the mode and hot-path lines, counters, the per-hook latency table and the
§2.4 budget table (`internal/commands/render.go`, `RenderStatus`).

**Meaning.** `source:` tells you whether you are reading a live daemon answer, the metrics file the
daemon last persisted, or nothing at all (`internal/commands/statuscollect.go`: `daemon`, `disk`,
`none`). A stale `disk` reading is not a current one.

In the same scratch project the banner read `host contract: no assertions reported`. That is not a
failure: `status` shows the assertion results the *daemon* holds, and no `SessionStart` hook had
ever run there, so there were none. A project that has never hosted a session has nothing to report,
and reports exactly that.

**Action.** No action; this is a recorded limit. See §2 for the `unavailable` latency rows.

### Qompack is inactive in the home directory

**Symptom.** A session opens with one line: "Qompack is inactive in this session: its project root
is your home directory, so it records nothing. Open a project directory (one with its own .git) to
use Qompack." Its MCP tools answer "qompack is inactive in this session: …" as a tool error, and a
command run from the same directory says `the project root is the home directory`.

**Diagnose.** `qompack doctor` from that directory: `scope.root` is `disabled` and its detail names
the directory. `qompack status` reads `source: none (unavailable)` with the same reason. The project
root is resolved as [architecture §2](architecture.md#2-write-set-and-retention) describes —
`QOMPACK_PROJECT_ROOT`, else the nearest enclosing `.git`, else the working directory — and three
things lead it to the home directory: the session was started there; it was started below a home
that is itself a git work tree (a dotfiles repository), in a directory with no `.git` of its own; or
`QOMPACK_PROJECT_ROOT` names the home directory.

**Meaning.** Owner decision D18. The store for such a root would be `~/.qompack`, the directory that
holds Qompack's user-wide configuration, the calibration file, the fallback logs and, on Windows,
the staged daemon copies, so Qompack refuses to treat it as a project. Nothing is recorded, no
daemon starts, nothing is written under `~/.qompack`, and every hook still exits 0: `SessionStart`
shows the line above and the other hooks answer `{}`. The comparison is made on the cleaned path,
ignores case on Windows and follows symlinks and junctions, so no spelling of the home directory
gets through (`internal/paths/home.go`). `fsck`, `backup`, `admin delivery-seal`, `self-test`,
`recall`, `pin`, `why` and `dropped` refuse with exit 1; `status` and `doctor` report and exit 0;
`config print` shows the user-wide configuration alone, labelled `user`, with the reason on stderr.
A project below the home directory, and the user-wide settings it inherits, are unaffected.

**Action.** Start the session in the project directory itself. Below a dotfiles home, a directory
without its own `.git` resolves to the home directory: run `git init` there, or set
`QOMPACK_PROJECT_ROOT` to it. A session that started in the home directory stays inactive even if
the work moves into a project below it, so the line it showed stays true; start a new session in the
project.

A build before this refusal recorded such sessions into `~/.qompack` itself, so after an upgrade that
directory may still hold a project store beside the user-wide files — `objects/`, `index/`, `spool/`,
`run/`, `state/` and the rest of the layout — and a daemon an older build started there may still be
running until its idle exit. This build never reads, writes or removes any of it. Whether to keep it
is yours to decide: `config.json`, `calibration.json`, `logs/` and, on Windows, `bin/` are the
user-wide layer; everything else there is the old home-directory sessions' history.

### `qompack config print --provenance`

**Symptom.** A setting does not appear to be doing anything.

**Diagnose.** The command prints the effective configuration as JSONC with an origin comment on
every leaf, for example `"budgetTokens": 12000,  // default config.Defaults()` (observed on this
tree). The five origins are listed in
[docs/config-reference.md](config-reference.md#provenance-origins). `qompack config schema` prints
the JSON Schema for the same keys.

**Meaning.** A leaf whose comment says `default` was not set by any layer you edited — the merge is
per leaf, so a project file that sets one key inherits every other default. A leaf that reads
`default` when you expected `project` is either in the wrong file or was not applied; §6 lists the
ways a value is accepted and not applied.

**Action.** Compare the origin against the layer you edited, then read the violations file below.

### `.qompack/state/config-violations.json`

**Symptom.** You edited a config file and nothing changed.

**Diagnose.** Read the file. It is the §11.3 record of every leaf-level fallback
(`internal/cli/config.go`, `configViolationsFile`), written by any command that loads configuration
through `LoadConfigAndReport` with a project root, and by every hook whose project already has a
`.qompack/` (`reportCaptureConfig`). A whole-block reset for a newer `settingsVersion` is written
here by the hook path only; `config print` reports it as a day-log warning — see
[§6](#6-configuration-and-schema-compatibility). On this tree, a project file
setting `runtime.mode` to `sideways` and `runtime.phase7.reuse.scopedCandidates` to `true` produced
exactly two entries:

```json
[
  {
    "Key": "runtime.mode",
    "Message": "invalid value, using default: sideways not in auto|full|passive|off",
    "Got": "sideways",
    "Want": "auto|full|passive|off"
  },
  {
    "Key": "runtime.phase7.reuse.scopedCandidates",
    "Message": "invalid value, using default: true not in false",
    "Got": "true",
    "Want": "false"
  }
]
```

On this build only a read command such as `config print` writes the first of those entries. A hook in
that project records nothing and writes neither, because a `runtime.mode` the hooks cannot apply as
written refuses capture instead of falling back (§6).

**Meaning.** This file carries two of §6's classes only. The first is an invalid value that fell
back (`internal/config/validate.go`, `ViolationsFromWarnings`, which selects warnings whose message
begins `invalid value, using default: `). A refused gated switch is recorded here as an invalid
value, which is why the second entry reads `true not in false`. The second, written by the hook
path only, is a newer-`settingsVersion` reset: on this tree a project setting
`runtime.migration.settingsVersion` to `99` gained an entry whose `Key` is `runtime.migration`,
whose `Message` begins `settingsVersion 99 is newer than this build understands (1); the whole
runtime.migration block is reset to defaults`, and whose `Got` and `Want` are `null`. Unknown keys,
values of the wrong type and retired-meaning keys are warnings but **not** violations, so they never
appear in this file.

**Action.** For the warning classes, see §6, and read `self-test`'s `config.capture` row for what the
hooks made of the same files.

### The log file

**Symptom.** You want the warnings that did not reach a file you have read yet.

**Diagnose.** `.qompack/logs/qompack-YYYYMMDD.log` (observed: `qompack-20260914.log` in the scratch
project). Every configuration warning is written there at `warn` level and every violation at
`loud` level (`internal/cli/config.go`, `LoadConfigAndReport`, and `reportCaptureConfig` for a
hook, which writes only once `logs/` exists). `internal/logging/logger.go`
documents that a `Loud` call also appends to `LOUD.log` in the same directory — append-only and
never rotated — and to a process-wide ring that `qompack status` prints as `recent loud lines`.

**Meaning, and a real gap.** `qompack config print` loads configuration with a no-op logger
(`internal/cli/commands.go`, `loadForCommand`, which passes `logging.Nop()`), so its warnings reach
no file at all. Observed on this tree: running `config print` over a config file with an unknown key
produced empty stderr, no log directory, and no mention of the key anywhere — while the violations
file was still written for the leaves that fell back. **A silent `config print` is not a clean
config file.**

**Action.** Run `qompack self-test` and read its `config.capture` row, which names every key the hooks
did not apply, or run a hook in the project and read the day log, rather than trusting `config
print`'s silence. `self-test`'s own `config.load` check logs nowhere either.

## 2. Unknown capability or telemetry

**Symptom.** A contract row reports something you did not expect, or a latency cell says
`unavailable`.

**Diagnose.** `qompack self-test` for the assertion rows; `qompack status` for the latency tables.

**Meaning — an assertion that reports unknown.** Covered in §1: `not-yet-implemented` is the
producer-absent state, and `first-session`, `no-precompact-pending`, `no-transcript-path` and
`unset` are the assertion running with nothing to judge. None of them is a verified host contract.

**Meaning — "degraded to passive recording".** When an assertion fails at critical severity the
monitor degrades the session and says so loudly; two consecutive clean runs restore it
(`internal/contract/monitor.go`, `RunAll`). `internal/contract/mode.go` defines what the degraded
mode is: L0 and L1 keep running — "observe, chunk, store, sketches, DAG, verbatim capture,
elimination records — the store stays correct and the session's data is not lost" — and everything
that *acts* is off: no `additionalContext` injection, no scheduler-initiated checkpoints, no drop
report. (The PreCompact focus instruction, `customInstructions`, is not on that list any more
because no mode emits it: no host accepts one, so it is retired — C1.18,
[docs/cannot-do.md](cannot-do.md#no-summarizer-model-substitution).) `qompack status` prints the
mode as `degraded-passive`.

A `SessionStart` whose hook could not reach the daemon in time — a cold daemon on a loaded machine is
enough — is spooled and replayed later, after the hook has answered without it. The replay mints no
`hook.additional_context_delivered` probe, and withdraws the probe of an answer that reached its hook
too late, so such a start cannot produce "sentinel not found after two chances"; only the prompts
of the session a probe was minted for, sent after it, count as its chances, and each prompt counts
once however many times its delivery is replayed. Nor can a replay fail
`session_start.source_compact`: a replayed `SessionStart` fired before a pending `PreCompact` is not
taken for the start it announced, and a replayed `PreCompact` whose compact `SessionStart` has
already arrived does not re-arm it. If a replay is what degrades the project, its banner appears on
the next `SessionStart` instead ([docs/architecture.md](architecture.md#1-process-model)).

**Meaning — `unavailable` latency rows.** Observed on this tree, every per-hook row in a fresh
project read `unavailable` with its own reason, for example:

```
  PostToolUse        qompack observe tool             B-A     unavailable
                       no per-hook instrument: PostToolUse is folded into the hook_controlled aggregate,
                       which mixes every delivering hook and cannot be attributed to one
```

and the B-D budget row read `no instrument records this histogram in this build; it measures host
process creation, which the created process cannot observe`. Both of those reason strings are
`internal/commands/statuscollect.go`'s. Why the cell carries a word instead of a borrowed number is
`internal/commands/render.go`'s, on `latencyText`: "An absent measurement prints the availability
word, never a zero."

**Action.** No action; these are recorded limits. A missing number is a missing number.

**Telemetry.** Nothing is sent anywhere. `runtime.telemetry.enabled` is hardwired off — a `true`
value is refused as a violation (`internal/config/validate.go`) — and the key
"exists only to say so" ([docs/config-reference.md](config-reference.md#runtime)).

## 3. Capture gaps

**Symptom.** Retrieved content is shorter, emptier or stranger than the thing you remember.

**Diagnose.** Read the record's `Fidelity`. The eight values and what each means are tabulated in
[docs/user-guide.md](user-guide.md#fidelity-coverage-and-error-states); the actions are here.

| Fidelity | What to do about it |
|---|---|
| `exact` | nothing — but "exact" is the *captured host delivery*, not the completeness of the underlying file, process or native conversation |
| `prefix` | the beginning was kept; ask for the rest only if a later capture holds it, because this one does not |
| `partial` | do not infer from the shape of the gap — the missing part is not necessarily at the end |
| `truncated` | a size bound cut it; the remainder was never stored, so no retrieval recovers it |
| `redacted` | privacy policy removed something *before* storage, so it cannot be recovered from Qompack at all (`internal/redact`, and `runtime.redact.enabled` defaults `true`) |
| `binary` | opaque; do not search or quote it as text |
| `failure` | there are no trustworthy bytes — treat the record as evidence that a capture was attempted, and nothing more |
| `unknown` | nothing recorded a fidelity; assume nothing in either direction |

**Meaning.** A gap is a gap in the archive, not a question about the current file. There is no
live-disk fallback anywhere in retrieval, and none may be added
([docs/user-guide.md](user-guide.md#current-vs-historical-reads)): substituting current disk
contents for a missing historical original would bypass every capture-time policy the original went
through, and is the defect the contract forbids. **Nothing is reconstructed from current files.**

**Subagent work.** A child agent's detail enters the archive through one door: `internal/observer`'s
step 8, "On `SubagentStop`, capture the subagent's returned summary and its tool-result hashes, so
the parent gains a retrieval path into detail it never held" (`internal/observer/doc.go`). If that
hook did not fire, the child's work was not captured by another route.

**Interrupted output — unknown.** Whether an interrupted tool call delivers a partial payload to the
hook at all, and therefore whether it lands as `prefix`, `partial` or nothing, was not probed on
this tree; Qompack records what the host delivers and has no separate notion of an interruption.
To settle it: interrupt a long-running tool call in an installed host session, then read that
record's fidelity through `expand`.

**Action.** Treat fidelity as the answer to "what may I quote from this?", and stop there.

## 4. Denied or unavailable evidence

**Symptom.** A tool answered, and the answer was not content.

**Diagnose.** Read the evidence outcome. `internal/core/evidence.go` declares exactly seven:
`ok`, `absent`, `unavailable`, `denied`, `corrupt`, `expired`, `uncertain`.

**Meaning.**

- **`denied` is not empty.** A policy or privacy decision refused this read. Something exists and
  you may not have it. Reading it as "there is nothing here" inverts the answer. A reason that names
  "the host's current permission rules" means a `Read` deny or ask rule in a Claude Code settings
  file matches the archived path today; the archive follows the rule, so change the rule, not the
  query. An `unavailable` whose reason begins "host policy unavailable" means one of those settings
  files exists but could not be read or parsed, or that together they hold more than 5000 Read path
  rules; every answer with a file path is withheld until it is fixed
  ([docs/security.md §1](security.md#1-trust-boundaries)).
- **`unavailable` is not `absent`.** The lookup failed, so the answer is *unknown*. It is never a
  licence to conclude the thing does not exist, and — per
  [docs/mcp-tools.md](mcp-tools.md#already_tried) — an `unavailable` or unrecognized state "never
  establishes absence or prohibits an approach".
- **`expired` and `corrupt` are different from both.** `expired` means retention removed it: it was
  there, and it is not recoverable from here — the same thing the coverage value `expired_deleted`
  records about where it lived.
  `corrupt` means it was found and could not be read — a storage fact, not an absence.
- **`absent` is the only one that asserts non-existence,** and it is assertable only with complete,
  fresh coverage; anything less is `uncertain` or `unavailable`
  ([ADR 0013](adr/0013-migration-contracts.md), which names "errors read as absence" as a defect).

**What `already_tried`'s `unavailable` requires of the caller.** Its three legacy answers are
absent, active and stale; `unavailable` was *added*, so a client with a closed three-state enum will
not have a branch for it. [docs/mcp-tools.md](mcp-tools.md#already_tried) requires that clients
"treat unavailable or unrecognized states as unknown, never as absence or a prohibition". Concretely:
a failed `already_tried` tells you nothing about whether the approach was tried, and it does not
authorize retrying as though it were fresh ground either.

**Action.** Re-ask, or proceed on your own judgement — but record which of those you did, because
the distinction is the whole value of the outcome.

## 5. Retrieval that looks wrong

**Symptom.** `re_read` returned something that does not match the file on disk.

**Meaning.** That is the design, not a bug. `re_read` reads the store's own version history and is
"never a live read of disk" ([docs/mcp-tools.md](mcp-tools.md#re_read)). Its response names
`source: store` — the only value the field ever takes — beside the `at` selector it resolved and the
`turn` the version was captured at. "Newest captured" is not "what is on disk right now".

**Action.** If you need the current file, ask the host to read it; that is a separately authorized
operation and Qompack does not perform it.

---

**Symptom.** A span looks truncated.

**Meaning.** Spans are minimal by default: a content tool returns the smallest chunk-aligned span
covering what you asked for, widened to a symbol boundary where one is known
([docs/mcp-tools.md](mcp-tools.md#expand)).

**Action.** Page with the `next_span` in the response, or pass `full: true` when you genuinely need
the whole object. Note that `full: true` returns the whole *available* object — fidelity and
coverage still qualify it.

---

**Symptom.** An elimination shows as `[active]` and you believe it is stale.

**Meaning.** A record goes stale when a path in its `depends_on` set changes
([docs/mcp-tools.md](mcp-tools.md#record_eliminated)). But there is a recorded partial here, and it
is not hypothetical: `plans/V5-report.md` §22 item 26 records that "with the observation ledger
blind, the digest renders an MCP elimination record `[active]` from the checkpoint's frozen copy
while its real state is stale and `already_tried` reports unavailable". It is routed to the
digest owner and unruled, and §22 item 27 records a second provenance defect beside it — pin records
stamped `mcp` because the slash-command ingest path has no production caller.

**Action.** Treat an elimination state read out of a digest as a claim that has not been verified end
to end; call `already_tried` for the authoritative answer, and apply §4 to whatever it returns.

---

**Symptom.** After a compaction, something you expected is missing from Qompack's rehydrated block,
or section 7 ("No longer in context") ends in `… and N more; call dropped()`.

**Meaning.** The block is held to 9,500 characters so that Claude Code delivers it whole: past its
10,000-character hook-field cap the host would hand Claude a saved-file path and a 2,000-character
preview instead ([docs/cannot-do.md](cannot-do.md#the-host-delivers-at-most-10000-characters-of-injected-context-whole)).
Records are chosen in a fixed order and kept whole, so on a long session some are left out on
purpose. Each one left out is named in section 7 with the call that restores it; the counted tail
means the section itself ran out of room, not that anything went unrecorded. Raising
`runtime.rehydrate.maxTokens` does not change this: the character ceiling binds first.

**Action.** Make the call section 7 names for the record you need — `why`, `re_read`, `expand`,
`already_tried` with the quoted target and approach, or a `Read` of the rule, skill or checkpoint
file — or `dropped()` for the complete list, which is read from
`.qompack/state/rehydrate-<session>.json` and is never truncated. If the resumed session instead
shows a `<persisted-output>` note with a file path, or `.qompack/logs/LOUD.log` has a line saying
"hook output exceeds the host's per-field cap", that is a defect: the rehydration is built never to
reach the cap. Report it with that log line.

## 6. Configuration and schema compatibility

There are five ways a configuration value can be *accepted and not applied*. Both loaders treat them
the same way, per leaf: `config.Load`, which every read command uses, and `config.LoadForCapture`,
which every hook uses. The hook loader is stricter in two ways — input it cannot read safely, and a
setting of either control over what is recorded, `runtime.redact` or `runtime.mode`, that cannot be
applied as written — and they are the subsection below, because they are the entry on this page most
likely to be the answer when nothing is being recorded.

| Class | What happens | Where you see it |
|---|---|---|
| invalid value | the leaf falls back to its default, loading continues | `config-violations.json`, `loud` in the day log |
| wrong type — a string where a number belongs, an unparseable `QOMPACK_*` or `--set` value, a section that is not an object | that value is ignored with a warning, and the leaf keeps the value from the layer below: the default when no lower layer set it | `warn` in the day log only |
| unknown key | a warning, never an error | `warn` in the day log only |
| newer `settingsVersion` | the whole versioned block is reset to defaults, so unknown future switches stay off | `warn` in the day log; the hook path also records it in `config-violations.json` (§1) |
| retired meaning | the value is still applied, with a deprecation warning naming the file and line | `warn` in the day log only |

On the hook path the first three rows do not apply inside `runtime.redact` or to `runtime.mode`: a
problem there refuses capture instead (below). `config print` and every other read command still
fall back for them.

`qompack self-test`'s `config.capture` row and `qompack doctor`'s `config.capture` row (in its
recording-gaps section) report what the hooks made of your files: `applied as written`, or a
warning that names every key they did not apply.

The versioned blocks, the gated switches and the retired-meaning keys are tabulated — generated from
the shipped code, so they cannot drift from it — in
[Versioned blocks](config-reference.md#versioned-blocks),
[Gated switches (ship off)](config-reference.md#gated-switches-ship-off) and
[Retired-meaning keys](config-reference.md#retired-meaning-keys).

**Gated switches refuse `true`.** Setting one from any layer is refused at load, the leaf falls back
to `false`, and the refusal is recorded as an invalid value (see §1's violations file). Editing a
config file cannot enable a capability this build does not support. One capability has no config key
at all: `store.migrate.legacyImportCutover` is a build gate
([Build gates](config-reference.md#gated-switches-ship-off) lists it), and §9 covers what its being
closed means.

### When the hook path refuses a configuration outright

**Symptom.** Nothing is being recorded — in the most visible form, `<project>/.qompack/` holds nothing
but `config.json` — while every hook still prints `{}` and exits 0, and `qompack config print`
prints a sensible configuration.

**Diagnose.** Run `qompack self-test`. Its `config.capture` row asks the hook loader itself and fails
**critically**, exit 1, when that loader refuses; `DETAIL` names the class of problem and never
echoes a value. `qompack doctor` reports the same verdict as its `config.capture` row, and a hook in
a project whose `.qompack/logs/` exists appends the same text to `hook-quiet-YYYYMMDD.jsonl` there.

**Meaning.** The hook loader (`internal/config/capture_load.go`, `LoadForCapture`) applies the table
above per leaf, exactly as `config.Load` does, but refuses the whole capture — no delivery admitted,
nothing recorded, no daemon started for it — for the problems no fallback can make safe. Each
refusal names its class:

| `DETAIL` names | Why it is a refusal and not a fallback |
|---|---|
| `the project config file is not a single strict JSONC object` (or `the user config file …`) | a file that does not parse — a syntax error, a duplicate key, an empty trailing comma, an unterminated comment, invalid UTF-8 — cannot be applied per leaf, because nobody can tell which of its leaves were privacy rules. `config.Load` drops the whole layer with a `loud` warning instead |
| `a runtime.redact setting cannot be applied as written` | an unknown key, a wrong type or an unparseable `QOMPACK_*`/`--set` value anywhere inside `runtime.redact`. Every fallback there would record under a weaker privacy policy than the one you wrote, so it fails closed, exactly as a redaction pattern that does not compile already does |
| `a runtime.mode setting cannot be applied as written` | a value outside `auto\|full\|passive\|off` (`"OFF"`, `"sideways"`), a wrong type (`false`, `0`) or an unknown `--set` key under `runtime.mode`, from any layer. `runtime.mode` is the capture switch, and its fallback — `auto`, not a lower layer's value — would record in a project whose operator may have been switching recording off, so it fails closed: the hooks do what `off` would. A valid value applies as before |
| `the project config file is not a bounded regular file` (or `the user config file …`) | a directory, a link, a file over 1 MiB, one that exists but cannot be opened (a permission refusal), one swapped for any of those between being checked and being opened, or one that was there when checked and then kept reappearing without ever opening for 250 ms. An editor's save that renames a new `config.json` over the old one is not a swap: the hook reads the old file or the new one (or, for a moment on Windows, finds no file: [architecture §2](architecture.md#2-write-set-and-retention)). `config.Load` follows a link and reads a large file, and reports a directory or a file it cannot open with a `loud` `unreadable config` warning, dropping that layer |
| `an environment or --set value exceeds the capture bounds or is not UTF-8` | one value over 64 KiB, all of them together over 1 MiB, or invalid UTF-8 |

Observed on this tree, running the `observe prompt` hook once in each of twelve scratch projects with
one project config file each, then `qompack self-test`
(`plans/sdd/V6-closeout/config/runs/repro-after-f846f91-windows.log`):

| Project config | Delivery recorded | `config.capture` |
|---|---|---|
| `{}` | yes | `ok`, `applied as written` |
| `{"runtime":{"notAKey":1}}` | yes | `warn`, names `runtime.notAKey: unknown key` |
| `{"runtime":{"migration":{"settingsVersion":99}}}` | yes | `warn`, names the `runtime.migration` reset |
| `{"runtime":{"mode":"sideways"}}` | **no** | critical, `a runtime.mode setting cannot be applied as written` |
| `{"runtime":{"phase7":{"reuse":{"scopedCandidates":true}}}}` | yes | `warn`, names the refused gated switch |
| `{"scheduler":{"youngDaly":{"enabled":true}}}` | yes | `ok`, `applied as written` |
| `{"checkpoint":{"budgetTokens":"12000"}}` | yes | `warn`, `invalid type for checkpoint.budgetTokens: expected int` |
| `{"runtime":{"mode":"sideways","notAKey":1}}` | **no** | critical, `a runtime.mode setting cannot be applied as written` |
| `{"runtime":{"redact":{"patterns":"PRIVATE-[A-Z]{12}"}}}` | **no** | critical, `a runtime.redact setting cannot be applied as written` |
| `{"runtime":` | **no** | critical, `the project config file is not a single strict JSONC object` |
| `{"runtime":{"mode":"OFF"}}` | **no** | critical, `a runtime.mode setting cannot be applied as written` |
| `{"runtime":{"mode":false}}` | **no** | critical, `a runtime.mode setting cannot be applied as written` |

Every hook run printed `{}` and exited 0, and `config.load` read `ok` in all twelve. **`config.load`
passing does not mean the hooks can load your config; `config.capture` is the row that says.**

**Action.** Repair what `config.capture` names, then re-run `qompack self-test` until that row reads
`ok` — or `warn`, if you accept the keys it lists — and run a hook: a new file under
`.qompack/spool/`, or the layout appearing, is the confirmation. For a `warn`, check each key it
names against [docs/config-reference.md](config-reference.md), which is generated from
`config.Defaults()` and is therefore the exact set of keys this build knows.

**After a plugin downgrade or upgrade.** Read the three sections linked above in the *new* build's
copy of the reference, in this order: [Versioned blocks](config-reference.md#versioned-blocks) (a
file written by a newer build has its block reset, silently as far as `config print` is concerned),
[Gated switches (ship off)](config-reference.md#gated-switches-ship-off) (a switch that was `true`
in a build where its gate had passed is refused in one where it has not), and
[Retired-meaning keys](config-reference.md#retired-meaning-keys) (still applied, with a warning, and
no longer meaning what the old documentation said). Then run `qompack self-test`, read
`config.capture`, and confirm a hook records, per the action above.

## 7. Daemon problems

**Symptom.** `qompack admin delivery-seal` refuses to run.

**Diagnose.** Read its message. Observed on this tree, in a scratch project with a daemon running
(the absolute project path is elided below as `<project>`):

```
qompack admin delivery-seal: delivery-seal: a daemon is running in <project> and owns the journals; stop it first: qompack: daemon lock already held
```

Exit status 1.

**Meaning.** The tool takes the per-project singleton lock and holds it for its whole run, so it
refuses a project a daemon is serving (`internal/daemon/delivery_seal_tool.go`). This is the
expected answer beside a running daemon, not an exceptional one.

**Action.** Stop the daemon and rerun the identical command — see "how to stop it" below.

---

**Symptom.** For a while, hooks fall back to the spool although nothing failed.

**Diagnose.** `qompack status` lists `daemon: delivery journal rotated; leases and acknowledgements
waited for the archive` among its recent loud lines, with the segments it moved between and the
pause in `pause_ms`, and its counters include `delivery_rotations` and `delivery_rotation_pause_ms`
(the pause's distribution is the `delivery_rotation_pause` histogram). `LOUD.log` keeps a line for
every rotation. After the daemon stops, `qompack doctor`'s `delivery.rollover` row carries the same
totals from `metrics/latency.json`. On disk, a rotation leaves a new directory under
`.qompack/state/delivery-segments/` and a new record at the end of
`.qompack/state/delivery-journal-log.jsonl`.

**Meaning.** The delivery journal rotated. Every 65,536 deliveries (or 64 MiB of journal) the daemon
archives the full window into `.qompack/state/delivery-generations/` before it assigns the next
identity, and leases and acknowledgements wait for it: 2.3 to 6.8 s per rotation of a full window in
the V6 close-out's measurements on loaded Windows and Linux hosts
(`plans/V2-WAVE1-carried-defects.md`, SP20-D4). The owner accepted this pause as a documented
residual (decision D6); moving the archive off the pause is deferred past this release. A hook that
cannot get its ACK within its deadline spools the delivery, and the drain leases it afterwards under
the same nonce, so nothing is lost or duplicated.

**Action.** None. Do not stop the daemon mid-rotation to "unstick" it: an interrupted rotation is
finished on the next start before anything else is assigned, and reported with `at_open=true`. If
the first rotation stopped between freezing the original seals and committing the switch, then until
the daemon starts again a store GC pass halts rather than collect, and `qompack fsck` reports a
frozen legacy-segment seal with no later segment named.

---

**Symptom.** The daemon log (`.qompack/logs/qompack-YYYYMMDD.log`) has a warning that `this
project's delivery journal will rotate for the first time soon`, or `qompack doctor`'s
`delivery.rollover` row says `the last daemon warned that this store's first rotation is near`.

**Meaning.** The project has never rotated its delivery journal and its lease journal has reached
three quarters of the rotation threshold (49,152 deliveries or 48 MiB). The first rotation is the
one step that cannot be undone: after it, a Qompack build older than segmented rollover refuses the
journal. The daemon warns once per run, at the point it is crossed or at the start of a run that
finds the project already past it, and counts it in `delivery_first_rotation_backup_advised`. The
warning is per run, not per project: every restart before the rotation warns again, so each run's
`delivery.rollover` row, which reads only that run's counters, still names the coming rotation.

**Action.** If you may want to run an older build on this project again, stop the daemon and take a
backup now (`qompack backup create`, [Backup and restore](backup.md)). A backup taken before the
first rotation is the only way back to such a build. Otherwise, nothing: the rotation happens on its
own.

---

**Symptom.** `LOUD.log` or `qompack status` shows `store: gc halted: the delivery journal carries
more archived leases without an acknowledgement than a pass can hold`, and `.qompack/` keeps
growing.

**Diagnose.** The status counter `store.gc.delivery_carry_over_bound` counts every halted pass, and
`qompack doctor`'s `delivery.rollover` row reads `degraded` with the number of halted passes. The
Loud line's `err` names the carried-lease file and how many leases it carries. The file is the
active segment's `delivery-carried-leases.jsonl` under `.qompack/state/delivery-segments/`; its
first line's `count` is the same number.

**Meaning.** Each rotation carries into the new segment every archived lease that has no
acknowledgement, so store GC can retain what those leases reference without reading old segments.
A GC pass harvests at most 65,536 carried leases; past that it halts and collects nothing, exactly
as for any retention source it cannot read, so nothing is deleted and disk use grows. The carry
shrinks only at a later rotation, by the carried leases an acknowledgement settled meanwhile. A
delivery retired by a policy denial is never acknowledged, and neither is a leased delivery that is
never published, so a project with many of those can stay halted. The line is Loud once per run of
halted passes, not on every idle tick; the counter counts every pass. The owner accepted this bound
as a documented residual (decision D6).

**Action.** There is no repair for it in this build. Do not edit or delete the carried-lease file or
any journal: that would release content a delivery may still need, or recycle observation
identities. Keep disk headroom, and keep `LOUD.log` and the doctor output for the report.

---

**Symptom.** Capture stops, and `LOUD.log` or `qompack status` shows `daemon: delivery journal
rotation refused: the archived leases without an acknowledgement would pass the carried-lease file's
bound`.

**Diagnose.** The status counters `delivery_rotation_failures` and
`delivery_rotation_carry_over_bound` each read at least 1, and `qompack doctor`'s
`delivery.rollover` row reads `degraded`, naming the 64 MiB bound. The Loud line carries
`carried_leases`, `carry_bytes` and `carry_bound_bytes`. Every later delivery also logs `daemon:
delivery identity unavailable; durable WAL retained for recovery`.

**Meaning.** The carry the rotation would have written (the store GC entry above) passes 64 MiB,
about 200,000 leases, which every reader of the file refuses. So the rotation refuses before it
stages anything, and the journal refuses every lease and acknowledgement from then on. A restart
finishes the same rotation at the open, meets the same bound, and refuses again (the Loud line then
reads `at_open=true`). Deliveries are kept in durable input (the ingest WAL, or the hook spool when
the daemon does not answer), so disk grows while it lasts, and nothing new is captured. Nothing is
half-written: the offline delivery check (`qompack admin delivery-seal --check` on the stopped
project, and the delivery row of `qompack fsck --seal-check`) still passes. The owner accepted this
bound as a documented residual (decision D6).

**Action.** There is no repair for it in this build. Stop the daemon and take a backup
(`qompack backup create`) to preserve the state, then report it with `LOUD.log` and the doctor
output. Do not delete or edit journals, carried-lease files or segments, and do not delete the WAL
or spool files: they are the only copy of the deliveries made since the refusal.

---

**Symptom.** An older Qompack build's `fsck` reports `state/delivery-lease-position.json parses as
JSON and carries no version`, and that build's daemon assigns no observation identities.

**Meaning.** The store has rotated its delivery journal, and its original seals are frozen so that a
build that predates segments refuses the journal rather than appending to it with arrival numbers
later segments already used. The current build reads the frozen seal as the archived segment 0 and
resumes each session at the arrival it left next. A build from before the V6 fail-closed journal
change still indexes deliveries in that state, without identities; the current build does not
revisit them.

**Action.** Use the current build. To go back to an older build, restore a backup taken before the
first rotation into a fresh destination ([Backup and restore](backup.md)); do not edit the seals.

---

**Symptom.** After a crash, the daemon refuses to open the delivery journal of a rotated store and
`qompack admin delivery-seal --check` reports a v2 seal with one torn slot in the active segment.

**Meaning.** The crash landed in the middle of the active segment's seal write. The daemon's reader
is strict and refuses the torn image.

**Action.** Stop the daemon, then run `qompack admin delivery-seal --check --accept-torn-slot --yes`
to see exactly which journal lines accepting the valid slot would admit, and
`qompack admin delivery-seal --to v1 --accept-torn-slot --yes` to repair the active segment's seal
at the position its journal's full scan recovers. The tool refuses Rule R on any archived segment.

---

**Symptom.** A daemon will not start, or a lock file looks orphaned.

**Diagnose.** `.qompack/run/daemon.lock` carries the owning `pid`, start time, address and version
as JSON, and `.qompack/run/daemon.hb` is its heartbeat (`internal/daemon/lock.go`).

**Meaning.** A lock is not reclaimed on age alone. `internal/daemon/lock.go` returns `ErrLockHeld`
while the liveness dial succeeds or the recorded process is still alive, and reclaims the lock only
once the heartbeat's mtime is older than its staleness window *and* neither probe finds a live
process — "rather than blocking every future daemon start forever".

**Action.** Do not delete `daemon.lock` to unblock a start. If a daemon really is gone, the
staleness protocol reclaims the lock by itself; if it is not gone, deleting the file removes the
protection against a second writer.

---

**Symptom.** After a compaction the model's context opens with "Qompack could not deliver this
compaction's rehydration" instead of the rehydration.

**Diagnose.** The note names its cause in parentheses. "it was not ready when the answer was due",
"building it failed", "the checkpoint store could not be read" and "the Qompack daemon was shutting
down" are the daemon's own:
`.qompack/logs/LOUD.log` has a matching `compact SessionStart answered without its rehydration`
line, `qompack status --json` counts it under `session_start_compact_deferred`, and
`.qompack/metrics/latency.json` has the route's phases — `session_start.contract`, `.compact_wait`,
`.finish` and `rehydrate.latest`, `.record` — which say where the time went. For "building it failed"
and "the checkpoint store could not be read", `LOUD.log` also has the failure itself: `rehydrate:
checkpoint unreadable` (with `checkpoint manifest unreadable` when the manifest is the cause),
`rehydrate: build failed`, or `rehydrate: panic recovered`; `qompack fsck` checks the checkpoint
store. "the Qompack daemon was shutting down" is no fault of the store or the build: the daemon was
stopping before the rehydration could start, or its stop cut the rehydration short (`LOUD.log` then
has `rehydrate: stopped before the rehydration was built`). "the Qompack daemon did not answer in
time" is the hook client's: no answer reached it within its reply deadline — 10 s, or less when
starting the daemon ran past its share of the hook's 15 s timeout (see the next entry) — so look at
whether a daemon was running at all (`qompack status`) and at the disk load at that moment.

**Meaning.** The rehydration is bounded (C1.16): the daemon waits for a compact rehydration for at
most a third of the hook's 15 s timeout, counted from the request's arrival, and a rehydration that
is not ready by then is answered with this note rather than with nothing. It finishes anyway, and
its drop report is recorded as undelivered, so `dropped()` says first that the whole rehydration
never reached the model. A rehydration that cannot be built at all is answered the same way (owner
decision D11): a checkpoint store the daemon cannot read — an unreadable `MANIFEST.jsonl`, or a
checkpoint whose bytes verify but do not decode — gets no rehydration built on top of it, and a build
that fails or panics has nothing to deliver. Each records a drop report whose first entry is the
whole rehydration, `not delivered: …`, naming the cause, so `dropped()` says it was never built. A
checkpoint that fails verification is not this case: the daemon steps over it to its parent, or
builds the rehydration without a checkpoint, and delivers that.

`dropped()` reports on the most recently *recorded* rehydration, which for a while can be an
earlier one, as the note itself says. A rehydration still being built has recorded nothing yet, and
one that a stopping daemon never started records nothing at all; one its stop cut short records a
report that opens `not delivered: the Qompack daemon was shutting down, so no rehydration was built`.
When the hook client wrote the note ("did not answer in time"), the daemon may still have answered,
too late, and recorded that rehydration as delivered; the client spools a request it got no answer to,
and when the daemon replays it — its client-spool watcher a few seconds after the spool is written,
else its idle drain once the project has been quiet for `scheduler.idle.detectAfterSeconds` (120 s
by default), or its next start — it records the rehydration as undelivered and says the hook answered without it. If the spool itself
could not be written, that correction never comes: `.qompack/logs/LOUD.log` then has an `ipc: spool`
line for the dropped request.

**Action.** Recover in the session: the note lists the calls — `expand` of the session's first
prompt, `recall` for anything captured, `dropped()` — and `.qompack/checkpoints/` holds the
checkpoint itself (the highest number is the newest). If it recurs, the machine's disk is the usual
cause: the route's own durable writes (`session_start.contract`, `.finish`) are fsync-bound.

---

**Symptom.** A session's first `SessionStart` got no answer: no §12.1 probe was minted for it, or
a compaction's context opens with the deferred note naming "the Qompack daemon did not answer in
time". It happens on a loaded machine, and more readily on Windows on the first start after the
plugin is installed or updated, when the daemon binary is first copied under the user's `.qompack`.

**Diagnose.** The project's day log (`.qompack/logs/qompack-YYYYMMDD.log`) has `hook: no time left to
wait for the daemon's answer; the request was handed to the spool` when starting the daemon used the
whole of `session-start`'s budget, with the overrun in `overrun_ms`, and `daemon: spawn failed` when
the daemon could not be started at all. If the spool could not write the request either,
`.qompack/logs/LOUD.log` has an `ipc: spool` line for the dropped request, or `hook: the request
could not be spooled and is lost`. Otherwise the start was spooled because the daemon was not
listening, or still replaying its spool, when the wait ran out. `qompack status` shows whether a
daemon is running now; until it has replayed the start, the request is a `session.start` line in a
`.qompack/spool/client-*.ndjson` file.

**Meaning.** `session-start` is bounded by its 15 s manifest timeout as a whole (V6 close-out D17b
and D21): the daemon must be listening within 8.25 s of the hook starting (or within 1.5 s of a
spawn that itself ran late), and its answer must arrive within the reply wait that follows — 10 s
when the daemon was up within 3.25 s, less when it came up later, and about the 5 s a compaction
may take when it came up just before 8.25 s. A start that misses either is
spooled, not lost: the daemon replays it — its client-spool watcher a few seconds after the spool is
written, else its idle drain once the project has been quiet for `scheduler.idle.detectAfterSeconds`
(120 s by default), or its next start — and records the session, but a replayed start mints no probe and delivers no rehydration, because
its answer could reach no one. Only one daemon is started per project however many hooks race to
start it (`.qompack/run/spawn.lock`); a second `qompack daemon` process that appears and exits at
once, because it cannot take the project's singleton lock, means a spawner found neither a daemon
answering its dial nor a spawn in flight while one was in fact starting or running: a spawn that
took longer than the spawn lock's 10 s freshness window, or a hook whose short dial a busy daemon did
not answer in time. Both are signs of heavy load.

**Action.** None for a single occurrence: the session continues, and a compaction's note lists the
recovery calls. If it recurs on every start, look at the machine's CPU and disk load when sessions
begin. On a loaded Windows machine the V6 close-out's cold-start diagnostic saw starting the daemon
process stall for 4 to 5 s in about one spawn in ten; what causes those stalls was not identified.

---

**Symptom.** On Windows, a plugin update or uninstall, or the host's cleanup of a `--plugin-dir`
extraction, cannot remove the plugin directory; `bin/qompack.exe` is "in use" or "Access is
denied", and the directory is left half deleted.

**Diagnose.** Find which executable the project's daemon is running from: the `pid` in
`.qompack/run/daemon.lock`, then its image path (Task Manager's details, or `Get-Process -Id <pid> |
Select-Object Path` in PowerShell). A current build runs it from
`%USERPROFILE%\.qompack\bin\<sha256>\qompack.exe`. If it runs from inside the plugin directory,
`.qompack/logs/LOUD.log` has a `daemon: running from inside the plugin directory` line, and the project's
day log in `.qompack/logs/` names why the copy could not be made when `session-start` started it.

**Meaning.** The daemon outlives the session by design, and Windows will not delete a running
executable or the directory holding it (C1.17). So whatever starts the daemon from the plugin's
binary — `session-start`, a hook's lazy spawn, or the `qompack mcp` server's — runs it from a
verified copy under the user's `.qompack\bin` instead, and only the session's own hook processes and
MCP server — which end with the session — ever run from the plugin directory. A daemon from a build
before this change, or one started after the copy failed (a full disk, an unwritable `.qompack`
under the user profile, a copy another program held open so that it could not be checked), still
holds the directory until it exits.

**Action.** Wait for the daemon's idle exit (below), or end that one process by its `pid`, then retry
the update or removal. If the LOUD line is there, fix what stopped the copy — the directory
`%USERPROFILE%\.qompack\bin` must be writable by you — and the next daemon start uses a copy.
Copies of versions no daemon is running are removed automatically when a new version is staged.

---

**Symptom.** A daemon is running and you want it to stop.

**Meaning.** It stops on its own when idle: `runtime.daemon.idleExitSeconds`
([default `1800`](config-reference.md#runtime)) is the number of seconds with zero live sessions
before the daemon exits (`internal/daemon/daemon.go`, `idleExitDue`). A session stops being live at
its `SessionEnd`, or, when the `SessionEnd` never arrived, after that same number of seconds with no
traffic from it — so a daemon can outlive its last session by up to twice the setting. On Windows
the process is the staged copy described above, not the plugin's own binary.

**There is no operator stop command in this build.** The daemon does have an `admin.shutdown` op,
but the only callers of `ipc.OpAdminShutdown` outside the daemon itself are the bench harness
(`test/bench/hotpath/transport.go`) and tests: no `qompack` subcommand sends it, and `qompack help`
lists no stop command. So the supported ways to end a daemon are to wait for its idle exit or to
terminate the process, identified by the `pid` in `daemon.lock`. No subplan currently owns an
operator-facing stop path.

**Action.** Prefer waiting for idle exit. If you terminate the process, terminate only the one whose
`pid` appears in that project's `daemon.lock`; daemons are per project and another project's daemon
is a different process.

---

### Windows Defender flags `qompack.exe`

**Symptom.** On Windows, Windows Security reports a threat in a Qompack binary, named
`Trojan:Win32/Bearfoos.A!ml` or `Trojan:Win32/Bearfoos.B!ml`, and blocks or quarantines the file:
the plugin's `bin\qompack.exe`, the daemon's staged copy under
`%USERPROFILE%\.qompack\bin\<sha256>\`, or a binary you built from source. A blocked binary does
not start: run by hand, Windows refuses it with "Operation did not complete successfully because
the file contains a virus or potentially unwanted software", and a quarantined one is simply gone.
The V6 close-out saw these detections on development builds of this tree (decision D32). Whether a
release build is flagged, and what Claude Code shows when a hook's binary will not start, have not
been observed.

**Diagnose.** Windows Security, Virus & threat protection, Protection history lists the detection's
name and the file it acted on. Then check that the file is the one the release shipped. Hash the
release zip you installed from and compare it with that zip's line in the same release's
`checksums.txt`:

```powershell
Get-FileHash -Algorithm SHA256 .\qompack-plugin-<version>-windows-amd64.zip
```

(`sha256sum --ignore-missing -c checksums.txt` does the same in Git Bash.) For an extracted bundle,
`bin/qompack.exe` is listed in the bundle's own `checksums.txt` ([install §1](install.md)). The
staged copy's directory is named by the copy's own SHA-256, and the daemon runs it only when its
bytes hash to that name and to the plugin binary it was copied from, so the name printed by
`Get-FileHash` on it must equal the directory's name and the `bin/qompack.exe` digest.

**Meaning.** The names end in `!ml`: the detection comes from Defender's machine-learning
heuristics, not from a signature of known malware. Qompack's binaries are not code-signed (an open
release item, [release §7](release.md#7-not-claimed)), and an unsigned, newly built executable that
spawns a background process and writes under the user profile is the kind of file such heuristics
flag. A binary whose digest matches the release's `checksums.txt` is byte-for-byte the one the
release published. One that matches nothing is not a Qompack release artifact: do not run it.

**Action.** For a binary that matches the release's checksums, report the false positive to
Microsoft through its file submission portal (<https://www.microsoft.com/en-us/wdsi/filesubmission>),
as an incorrectly detected file, naming the detection. Restoring the file from quarantine or adding a
Defender exclusion is your own decision: Qompack never adds one and needs none to be correct, and an
exclusion turns off scanning for everything under the path it names. If you do add one, scope it to
the one verified file or its directory. Qompack cannot work while its binary is blocked; until the
detection is resolved, remove or disable the plugin ([Safe disable](#8-safe-disable)).

## 8. Safe disable

Five steps, least invasive first. The first four are configuration keys, so each of those is
subject to §6 — a typo in the key or the value does not disable Qompack politely, it stops the
capture path entirely.

### Step 1 — stop injecting, keep recording

`runtime.migration.reinjection.sessionStartCompact = false`
([reference row](config-reference.md#runtime)) "disables injection without touching recording".

**What keeps being written.** Everything. Capture, the store, sketches, the DAG, records, the log —
all unchanged. Only the post-compaction `additionalContext` block stops being injected.

### Step 2 — `runtime.mode = passive`

This forces the mode the contract monitor would otherwise reach on a critical failure
(`internal/contract/monitor.go`, `RunAll`, `"passive"` arm).

**What keeps being written.** Per `internal/contract/mode.go`: L0 and L1 keep recording — observe,
chunk, store, sketches, DAG, verbatim capture, elimination records. What stops is everything that
acts: injection, scheduler-initiated checkpoints, the drop report. (No mode emits a PreCompact
focus instruction; it is retired — C1.18.)

### Step 3 — `runtime.mode = off`

The operator's own instruction to do nothing at all. It is a state the monitor never enters by
itself. `RunAll` returns before running a single assertion; `ipc.Client.Send`'s step 1 performs "no
dial, no spool write"; and `internal/cli/capture_admission.go` returns an empty capture before any
payload bytes are admitted.

**What keeps being written.** Nothing from the hook path. Observed on this tree: with
`{"runtime":{"mode":"off"}}` as the only project config, `qompack checkpoint` with empty stdin
printed `{}`, exited 0, and created no `.qompack/` layout at all. Read commands you run by hand
still write — `qompack status` and `qompack self-test` create the layout and start a daemon
whatever the mode says, because you asked them to.

Spell it exactly `"off"`. A value the hooks cannot apply as written — `"OFF"`, `false`, any other
value outside `auto|full|passive|off` — also records nothing from the hook path, because the hooks
refuse that configuration rather than fall back to `auto` (§6); but `self-test` then reports
`config.capture` as a critical failure rather than a clean off, and the read commands fall back to
`auto`.

### Step 4 — `runtime.daemon.enabled = false`

**What keeps being written.** The hooks still run and still write, but nowhere useful: every request
is spooled instead of sent (`internal/ipc/client.go`, `Send` step 2), and no daemon is spawned at
session start (`internal/cli/sessionstart.go`, `ensureDaemonRunning`, whose comment explains that
before this check existed a disabled-daemon project still got a resident process whose "idle drain
quietly processed the spool anyway"). Observed on this tree: `qompack checkpoint` with empty stdin
created a single spool file, `.qompack/spool/client-<pid>.ndjson` (the pid elided), with no `run/`
directory and no daemon. Nothing drains that
spool while the daemon stays disabled.

This is a *more* invasive setting than `mode = off` in one respect — it leaves work accumulating on
disk rather than declining it — so prefer step 3 if your goal is "stop doing anything".

### Step 5 — remove the plugin from Claude Code

Qompack runs because the host's plugin configuration installs its hooks and its MCP server; removing
the plugin through Claude Code's own plugin mechanism is what stops it being invoked at all, and no
Qompack command performs or simulates that. This page deliberately does not describe the host's
menus or file layout, because it cannot verify them from this repository.

**What keeps being written.** Nothing new. `<project>/.qompack/` stays on disk with everything
already recorded in it, and deleting that directory is the only way to remove it.

The install and uninstall procedure is in [docs/install.md](install.md).

## 9. Backup, rollback and recovery

The supported operator path is documented in [Backup and restore](backup.md). The `backup create`,
`backup verify` and `backup restore` commands use writer exclusion and restore into a fresh
project destination. Restore reads back with the same build and runs the packaged integrity checks;
it does not activate an older reader or discard writes made after the backup.

The legacy import/cutover gate remains closed. Historical V5 fixture rehearsals and Git rollback
records remain historical evidence; neither certifies an installed older release reading this
candidate's data. Human UAT, cross-version readers and supported-platform rehearsals must record
the actual candidate and backup identities. Missing or failed checks remain unverified.

Startup publication accounting surfaces incomplete captures and object candidates through status
counters and LOUD diagnostics. A bounded scan can be incomplete; zero observed gaps then means
only a lower bound. `fsck` inspects integrity but never promises that missing content was restored.

An uncertain observation-intent write stops new capture publication until the store is reopened
and checked. After resolving a transient storage error, restart the daemon to retry retained input.
A torn or conflicting intent may still prevent recovery after restart: preserve the original store
and use a verified backup in a fresh destination. No `fsck` option repairs these intents by truncation.

If a leased delivery loses its WAL/spool bytes, later deliveries in that session remain pending.
They cannot safely bypass unknown earlier context. Restore the matching source history or use a
verified backup; do not delete the lease to manufacture a fresh observation identity.

**Do not downgrade data to match old prose.** If a document describes a recovery step this build
does not implement, the document is the thing that is wrong. Do not delete, truncate or rewrite
anything under `.qompack/` to make a command or a page appear to work — `checkpoints/`, `pins/` and
`sketches/tried.bloom` are append-only by construction and `internal/paths` refuses to truncate them
([docs/architecture.md §2](architecture.md#2-write-set-and-retention)), which is a guard against
exactly that instinct.

## 10. What not to conclude

- **A passing `self-test` is not a verified host contract.** Any row whose `OBSERVED` column reads
  `not-yet-implemented` made no assertion at all, and the same is true of every spelling in
  `internal/contract/observation.go`'s `noObservationSpellings` — `first-session`,
  `no-precompact-pending`, `no-transcript-path` and `unset` among them. See §1.
- **`config.load ok` is not "your configuration works".** It exercises the soft loader, which falls
  back instead of failing. Whether the hooks can load the same files is `config.capture`, which
  fails critically when they refuse and warns when they drop a key. See §1 and §6.
- **An `unavailable` row is not a zero.** No number was borrowed and none was invented. See §2.
- **`unavailable` is not `absent`, and `denied` is not empty.** See §4.
- **A `dropped` report is qualified coverage, not proof of native eviction.** It is "Qompack's
  recorded omissions for this session" and "does not establish what remains in native context"
  ([docs/mcp-tools.md](mcp-tools.md#dropped)).
- **An `ephemeral` tag is a Qompack record property.** It is not a host eviction control and not
  proof of native context retention ([docs/mcp-tools.md](mcp-tools.md)); an ephemeral tag describes
  a Qompack record, it does not mean the host evicted anything.
- **A `re_read` that differs from disk is not stale data.** It is the store's version history. See
  §5.
- **Nothing on this page prescribes a status-line replacement**, and nothing on it claims a
  performance, cost or saving effect of any kind.

## Where to look next

- [docs/user-guide.md](user-guide.md) — what the commands, slash commands and tools do
- [docs/config-reference.md](config-reference.md) — every configuration key, generated
- [docs/commands.md](commands.md) — the slash commands and their exact flags, generated
- [docs/mcp-tools.md](mcp-tools.md) — the MCP tools and their argument schemas, generated
- [docs/cannot-do.md](cannot-do.md) — the capabilities this build does not have, and where each
  limit is recorded
- [docs/uat.md](uat.md) — the acceptance scenarios and the evidence a run has to leave
- [docs/architecture.md](architecture.md) — the contracts behind all of it, including
  [what is not supported](architecture.md#10-what-is-not-supported)
- [docs/adr/README.md](adr/README.md) — every architecture decision record, with its status

Packaging and release, added by SP-17:

- [docs/install.md](install.md) — installing, upgrading and uninstalling the bundle
- [docs/security.md](security.md) — the security and recovery posture
- [docs/release.md](release.md) — how a release is cut and what it claims to support

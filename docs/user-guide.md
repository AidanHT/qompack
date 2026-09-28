# User guide

This page explains what Qompack does in a session, what each command and tool reports, and how to
read the qualifications those reports carry. It does not restate the architecture — the contracts
live in [docs/architecture.md](architecture.md) — and it never restates a generated schema: the
command and tool pages are generated from the shipped code, so this page links them instead.

## Who this is for and what to expect

You are running a Claude Code session with the Qompack plugin installed, and you want to know what
it added, what it dropped, and how to get something back. (Installing the packaged bundle is covered
in [docs/install.md](install.md), and its security and recovery posture in
[docs/security.md](security.md). Building from source is covered in [README.md](../README.md).)

**What it adds.** Qompack records what the session produces into `.qompack/` through the host's
hooks, writes a checkpoint at `PreCompact`, and — after the compaction — injects one bounded,
checkpoint-derived block through `SessionStart` with `source=compact`
([docs/architecture.md §7](architecture.md#7-checkpoint-and-rehydration)). Everything else it
offers is on demand: six slash commands and eight MCP tools that read what was recorded.

Three things it does not do, each recorded where
[docs/architecture.md §10](architecture.md#10-what-is-not-supported) names it:

- **It never cuts, evicts or rewrites native context.** History rewriting and native eviction are
  `unsupported` and `Excluded` (`plans/MIGRATION-EVIDENCE.md`, "Capability decisions").
- **It cannot prove the model used what it injected.** Injection is recorded as "documented;
  installed behavior unknown", and nothing here observes what the model did with a payload — the
  `why` tool says the same of recorded reasoning: it "does not prove model compliance".
- **It promises no improvement.** `plans/V5-report.md` §26: no held-out task runs, stochastic
  baselines or quality trials were executed, and the admission quality row is "inconclusive by
  construction".

The full list is [docs/cannot-do.md](cannot-do.md).

**One warning before you run anything.** `qompack status` (and so `/qompack:status`) creates
`.qompack/` in the project directory it resolves and starts that project's daemon. It is a write.
See [Operator commands](#operator-commands) for exactly which commands do this.

**Open a project, not your home directory.** Qompack works per project: the project root is the
nearest enclosing `.git`, or else the working directory. When that root is your home
directory — a session started there, or started below a home that is itself a git repository (a
dotfiles repository) in a directory with no `.git` of its own — Qompack is inactive for the whole
session (owner decision D18): its store would be `~/.qompack`, which holds Qompack's own user-wide
settings. The session opens with one line saying so, the slash commands and MCP tools answer that
Qompack is inactive, and nothing is written anywhere. Start the session in the project directory
instead ([docs/troubleshooting.md](troubleshooting.md#qompack-is-inactive-in-the-home-directory)).

## Slash commands

Qompack installs six slash commands. Each shells out to the `qompack` binary, so
`/qompack:status` and `qompack status` are the same code. The inventory, every flag and the exact
help text are in [docs/commands.md](commands.md); this section says what each one is *for* and what
it can report.

Two things are true of all six, per that page's preamble:

- `--json` emits a versioned envelope instead of text; `--help` prints the usage block.
- Exit codes are `0` success, `2` a malformed invocation, `1` anything else. None of the six is a
  hook entry point; those are the only subcommands that always exit `0` (`internal/cli/dispatch.go`).

A third rule holds where a frontend finds nothing to answer from: it reports that it is unavailable
and exits `1` instead of inventing an answer. `/qompack:eval` in a project with no evaluation
artifacts is the everyday case.

There is no checkpoint command — see [Checkpoints are automatic](#checkpoints-are-automatic) below.

### `/qompack:status`

Reports mode, host contracts, store, per-hook latency and the last decision. Takes no positional
arguments; `[--json]` for the envelope ([schema](commands.md#qompackstatus)).

Status always succeeds where there is a report to show, because "nothing could be reached" is one
of its answers rather than a failure to produce one (`internal/commands/cmd_status.go`). Every
displayed value carries its own provenance (`internal/commands/statuscollect.go`):

- a **source** — `daemon` (live), `disk` (the last persisted metrics snapshot) or `none`;
- a **status** — `available`, `unavailable` (nothing observed this; no value is invented) or
  `error` (something answered and the answer could not be used);
- an **age**, which serializes as `null` when it is unknown rather than as zero;
- a **measure** — `observed` for a value an instrument recorded, `estimated` for one derived from
  an observation.

Exit `2` on an unknown flag; otherwise `0`.

### `/qompack:recall`

Searches captured tool output and file versions by content: `<query> [--k N]`, plus `--json`. The
query is free text or the prefixed selectors `path:<glob>`, `symbol:<name>` and `tool:<ToolName>`
combined with spaces ([schema](commands.md#qompackrecall)). It returns references and summaries,
not bytes — use the `expand` tool to materialize one. Capture and coverage may be partial, and the
response says which. Exit `2` on a missing or malformed query, `0` otherwise — including for a
search that matched nothing, which is an answer and not a failure.

### `/qompack:pin`

Pins an invariant so it is never summarized away: `<text>`. The same command manages the list
(`--list`, `--remove <id>`), records authority (`--source user|agent`) and records an eliminated
approach instead of an invariant (`--eliminated` with `--target`, `--approach`, `--reason`,
`--depends-on`, `--scope`). The full flag set is in [docs/commands.md](commands.md#qompackpin).

Pins are read directly from the append-only pin store, so `--list` answers even when the daemon is
not running (`internal/cli/qompack_commands.go`). Exit `2` if the flags do not form a valid
invocation, `1` if the pin could not be written, `0` otherwise.

### Checkpoints are automatic

There is no `/qompack:checkpoint` command, and a manual checkpoint is not offered yet. Qompack
writes a checkpoint automatically at `PreCompact`, just before every compaction, and on its own
cadence during a session once enough new work has accumulated
([docs/architecture.md §7](architecture.md#7-checkpoint-and-rehydration)). The block injected
after a compaction is built from the session's latest checkpoint, which is normally the one that
compaction sealed, so a checkpoint taken by hand earlier would be superseded before anything read
it.

Earlier builds shipped a `/qompack:checkpoint` command file that ran `qompack checkpoint`. That name
is the `PreCompact` hook entry point (`internal/cli/hooks.go`, registered `Hook: true`): it reads a
hook event from stdin and **always exits `0`**, so the command wrote no checkpoint and reported
nothing. It is no longer installed, and [docs/cannot-do.md](cannot-do.md#no-manual-checkpoint-command)
records the limit.

**Typing `qompack checkpoint` by hand is still a write.** Observed on this tree, running the built
binary with empty stdin in a fresh directory outside the repository: it printed `{}` and exited `0`
— and it created the full `.qompack/` layout there and started that project's daemon. No checkpoint
was written (`.qompack/checkpoints` was empty), but the empty payload was classified and recorded as
a capture, and the run's own state was persisted. It is a hook entry point, not a way to take a
checkpoint.

### `/qompack:why`

Explains a recorded decision and the evidence behind it: `<decision-id>`, a `dec_<12 hex>` id taken
from a checkpoint or from a rehydrated decision list ([schema](commands.md#qompackwhy)). What it
returns is an attributed record of a decision, not a claim about what the model then did. Exit `2`
without a decision id, `1` if the decision could not be read, `0` otherwise.

### `/qompack:dropped`

Reports what the last compaction dropped and how to get it back: `[--json]`
([schema](commands.md#qompackdropped)).

`dropped` is the command's name; what it reports is **qualified coverage**. Each entry carries a
`Coverage` value (see [Fidelity, coverage and error states](#fidelity-coverage-and-error-states)),
so "dropped" means *Qompack did not carry this forward in the injected payload, and here is what it
does hold instead*. It does not mean the host removed anything, and it is not a statement about
native context at all: `docs/mcp-tools.md` puts the boundary in one line — "This report does not
establish what remains in native context."

The complete drop report is persisted whether or not the rendered section fit in the budget
([ADR 0011](adr/0011-rehydration-budget-and-item-order.md), Consequences), so this command returns
everything even when the injected payload showed a counted line. Exit `2` on an unknown flag,
`1` if no drop report could be read, `0` otherwise.

### `/qompack:eval`

Reports the latest replay and live evaluation results: `[--corpus <path>]`
([schema](commands.md#qompackeval)).

It reads what the two evaluation producers left on disk and never runs either of them
(`internal/commands/evalartifacts.go`):

- the newest finished real-host run `devtool live-eval` wrote under `dist/live-eval/<run-id>/` —
  its `plan.json` and `summary.json`, the newest by the plan's creation time; a newer run that has
  a plan and no summary yet (still running, or stopped before writing one) is named in a note. Only
  the newest run's summary is read, so an older run's unreadable summary does not stop the report,
  while the newest run's own must be readable (`live-eval` writes both files whole, by a rename);
- the deterministic replay report the replay driver writes to `testdata/bench-replay.json`.

Both paths are relative to the project root and are build outputs of a Qompack source checkout.
`--corpus <path>` reads one artifact from anywhere instead: a single run directory, a directory of
runs, or a replay report file. With nothing to read the command says where it looked, reports
unavailable and exits `1`; an artifact that is there but unreadable is an error, never an empty
result.

The **baseline** is a named policy in the replay artifact — `eval.Report.Baseline`, "the Policy
every Regression is measured against" (`internal/eval/types.go`). The command's replay gates are
Qompack's own policies' — every scored policy whose name begins `qompack`, each judged:
`qompack-rehydrate`, the driver's default, leads with the plain gate IDs (`TASK-01`, `REC-02`, …),
and each further one (`qompack-l3`, when `--policies` asks for it) carries its name after `@`
(`TASK-01@qompack-l3`), so no Qompack policy the report scored can fail unseen. The other policies
a replay scores are references that bound the metric — `stock` is the baseline, `null` keeps
nothing, `oracle` is the Belady ceiling — and are named in a note, never judged; a report that scored no
Qompack policy is inconclusive. The command does not itself re-run the corpus, because running the
harness is `test/replay`'s job and a second driver would bring its own corpus selection
(`internal/commands/cmd_eval.go`). A replay is deterministic and model-free: it estimates what a
keep-set is worth, not what a model did.

A **live run** is reported with its qualification before any of its numbers: who executed it (the
run's own statement — agent-executed on the real installed host, never human UAT), the model and the
pre-registered model, the Claude Code version, the plugin bundle, the task set and fixture-tree
hashes, and the sample size. Each arm's task success, constraint-clean and recovery proportions carry
their confidence intervals, and cost is a list-price-equivalent **estimate** beside the host's own
figure. Its gates are `LIVE-T01` (task success, qompack − stock, under the pre-registered
non-inferiority rule), `LIVE-T02` (constraint-clean trials, failed only as a regression) and
`LIVE-R01` (recovery, reported and never judged). They are judged **only for a confirmatory run** —
a pre-registered task set whose file and fixture tree hash to the values its pre-registration froze
(`eval.LivePreregistrations`), the plugin loaded by `--plugin-dir`, both arms with no other plugin
loaded on either, the pre-registered model (or the pre-registration's one contingency alias with the
single model the hosts resolved it to recorded), every task including the held-out ones, every
planned trial, a bundle built from a clean tree, and a plan that attests the bundle carries no known
open defect (`devtool live-eval --known-open-defects none`, the pre-registration's section 9). That
attestation is the operator's word — a bundle cannot prove which defects it fixes — and the report
says so beside every run that carries one, so `confirmatory: yes` is never printed unqualified. Any
other run prints why it is not confirmatory and its gates read `not judged`
(`internal/commands/cmd_eval_live.go`). Most of these conditions are fixed when a run is planned, so
`devtool live-eval` prints that half (`eval.LivePlanDepartures`) and the bundle it would load before
any session starts, and with `--confirmatory` it refuses to plan a run that departs from them.

A pre-registered task set that its pre-registration superseded before use is never confirmatory:
`qompack-live-v1` gave way to `qompack-live-v2` (`testdata/eval/live/tasks-v2.json`, amendment A7)
before any trial of either, because two of its tasks could be passed by doing nothing and one
constraint check failed trials that never touched what it guards, and `devtool live-eval` refuses to
plan a run of it.

What it can report: a verdict of `pass`, `fail`, or `inconclusive` — a distinct outcome for a run
whose trials were skipped, or a replay whose trials failed, "because a gate that passes on an
evaluation which did not run is not a gate". A live run's failed trials do not by themselves make it
inconclusive: its pre-registered decision already counts every one of them (intention to treat — a
harness failure is scored as a failure on every outcome), so the verdict is that decision, and the
report lists each failed trial by name with how it was counted. When a confirmatory run's rule
reaches no verdict — inconclusive, or not-applicable because a trial's plugin state contradicted its
arm — the verdict is `inconclusive` even when a passing replay is read beside it, as plain
`qompack eval` does; a constraint regression still fails the run. A metric with no declared
threshold is printed and explicitly **not judged**, and cost never contributes to the verdict.

## MCP tools

Qompack exposes eight tools over MCP, on stdio, from `qompack mcp`. The inventory and every
argument schema are in [docs/mcp-tools.md](mcp-tools.md).

Four behaviours apply across the set, from that page and from `internal/mcp`:

**Ephemeral metadata.** Every retrieval response carries `_meta.qompack.ephemeral`.
An ephemeral tag describes a Qompack record; it does not mean the host evicted anything.
It is not an eviction control and not proof of native retention.

**Failed queries.** unavailable is not absent: a failed query leaves prior attempts unknown and never prohibits an approach.

**Minimal spans.** A tool that returns file content returns the smallest chunk-aligned span
covering the request, widened to a symbol boundary where one is known. Pass `full: true` for the
whole object; a response with more to read carries `next_span`, which you pass back as `span`.

**Misses are not errors.** A thing that was looked for and is not there comes back as
`found: false` with what was searched, not as a tool error (`internal/mcp/handlers_span.go`).

### `recall`

Searches captured archive material by content, path or symbol and returns references and
summaries. Call it when you need to find *where* something was, before spending budget on bytes.
It returns pointers rather than content, so it never counts as demand for promotion.

### `expand`

Materializes archived content by `hash` or `tool_use_id` — the identifiers a tombstone or a
`recall` hit gives you. Call it when a reference is not enough. Minimal span by default; `full:
true` or an explicit `span` when it is not; `next_span` to page. Fidelity and coverage may be
incomplete, and a query that fails to reach the store reports `unavailable`, which is not a
statement that the content is gone.

### `re_read`

Returns the latest captured, or a historical, version of a file from the store's own version
history. It never reads disk — see [Current vs historical reads](#current-vs-historical-reads).
Call it when you need a file as it was when Qompack saw it, at a turn, a timestamp, or a root hash.

### `already_tried`

Queries recorded elimination evidence for a `target` and an `approach`. Call it **before**
committing to an approach — that is the standing instruction Qompack injects alongside a non-empty
elimination list ([ADR 0011](adr/0011-rehydration-budget-and-item-order.md) §13). Answers are
`absent`, `active` or `stale`; a failed query is `unavailable`. Treat `unavailable` — and any state
you do not recognize — as unknown: never as absence, and never as a prohibition.

### `record_eliminated`

Writes negative knowledge: that an approach does not work, with the reason and the project-relative
paths the reason rests on, so it survives compaction. It is the one tool here that is **durable**
rather than ephemeral. A change to any `depends_on` path flips the record to stale rather than
deleting it. Check the response before relying on persistence.

### `timeline`

Retrieves recorded session segments over a turn or timestamp range. Call it to reconstruct order —
what happened between two points. Missing events and native-context coverage may be unknown.

### `why`

Retrieves an attributed decision and its evidence from the checkpoint chain, by `decision_id`. Call
it when you need to know *why* something was decided rather than *what* was decided. Recorded
reasoning does not prove model compliance.

### `dropped`

Retrieves Qompack's recorded omissions for the session, with coverage attached. Call it when
something you expected to be present is not. As with the slash command, it does not establish what
remains in native context.

## Current vs historical reads

Every content answer Qompack gives is a read of its own archive. There is no live-disk path in any
of the eight tools, and none is added as a fallback.

`re_read` is the one that looks like a current read and is not. Its `at` argument selects a version
from the store's own history: empty for the newest **captured** version, or an RFC3339 timestamp, a
`sha256:<hex>` root, or `turn:<N>` (`internal/mcp/handlers_span.go`, `resolveVersion`). "Newest
captured" is not "what is on disk right now": a live read would bypass every capture-time policy —
redaction, size bounds, host-denied paths — that a real capture went through, and substituting
current disk contents for a missing historical original is the defect the contract forbids. A
current-file read remains the host's own, separately authorized operation.

`recall` and `expand` return archive material in the same way, qualified by fidelity and coverage.

**How to tell what you are holding.** A `re_read` response names where the bytes came from:
`source`, whose only value is ever `store`, because there is deliberately no worktree counterpart
(`internal/mcp/handlers_span.go`, `sourceStore`). It is the one tool that sets the field, and it is
the one tool that could otherwise be mistaken for a live read. Beside it, `re_read` reports `at`
(the version selector that was resolved, omitted when the call did not use one) and `turn` (the
turn the version was captured at), so `source: store` with a `turn` is a historical read of one
specific captured version.

An `expand` response carries no `source` at all — the field is omitted when empty, and `expand`
sets none, because it is addressed by `hash` or `tool_use_id` rather than by a point in a file's
history. What it gives you instead is the `hash` it resolved, the `span` it returned, and the
`_meta.qompack` fields every content tool publishes (`span`, `total_bytes`, `truncated`,
`next_span` where there is more, `path` where one is known, `ephemeral`). Both tools also report
`widened`, and both answer exclusively from the archive.

So the discriminator is not one field on every response: it is that **no** response shape means
"read from disk just now". If you need the current file, ask the host to read it.

## Fidelity, coverage and error states

Three enumerations travel with retrieval evidence (`internal/core/evidence.go`). Read them
together: fidelity is about the bytes, coverage is about where the thing was found, and the outcome
is about whether the question could be answered at all.

**Fidelity — what happened to the retained bytes.** "Exact" means the captured host delivery, not
completeness of the underlying file, process or native conversation.

| Value | What it means for you |
|---|---|
| `exact` | the bytes are the host delivery as captured |
| `prefix` | the beginning was kept and the rest was not |
| `partial` | some of the content is missing, not necessarily from the end |
| `redacted` | privacy policy removed something before storage |
| `truncated` | a size bound cut the capture |
| `binary` | not text; treat it as opaque |
| `failure` | the capture itself failed; there are no trustworthy bytes |
| `unknown` | nothing recorded a fidelity, so assume nothing |

**Coverage — what the record says about where the thing lived.** It never describes complete native
history.

| Value | What it means for you |
|---|---|
| `qompack_included` | Qompack carried this forward in what it injected |
| `archive_only` | it is in the archive, and was not carried forward — ask for it |
| `native_load_observed` | a native load of it was observed; this is an observation, not a guarantee it is still there |
| `expired_deleted` | retention removed it; it is not recoverable from here |
| `unknown` | coverage was not established — not a claim either way |

**Outcome — whether the question could be answered.**

| Value | What it means for you |
|---|---|
| `ok` | answered |
| `absent` | genuinely not there; assertable only with complete, fresh coverage |
| `unavailable` | the lookup failed; the answer is unknown |
| `denied` | policy refused this read; something exists and you may not have it |
| `corrupt` | found and unreadable |
| `expired` | retention removed it |
| `uncertain` | answered without enough coverage to say `absent` |

Two rules follow, and both are easy to get wrong:

**`unavailable` is not `absent`.** [ADR 0013](adr/0013-migration-contracts.md) names "errors read as
absence" as a defect and sets the rule: `absent` may be asserted only with complete, fresh
coverage; anything less is `uncertain` or `unavailable`, with a reason and a recovery direction. A
failed `already_tried` tells you nothing about whether the approach was tried.

**A denied read is not an empty read.** `denied` means a privacy or policy decision refused the
content — the record exists and was withheld. Reading it as "there is nothing here" inverts the
answer. Nothing substitutes current content for a denied or unavailable one.

## Current-authority corrections

`internal/state` separates what Qompack observed from what it believes. A record is a statement
derived from observations, and **content cannot promote its own authority**
(`internal/core/evidence.go`). These are the levels:

| Authority | What produced the statement |
|---|---|
| `user_correction` | you corrected it; the highest authority there is |
| `explicit_decision` | a decision was stated outright |
| `tool_observation` | a tool result was observed |
| `candidate_extraction` | something was extracted from text as a candidate |
| `hypothesis` | a guess, held as a guess |
| `conflict` | two records disagree and the disagreement is recorded rather than resolved |

So a `user_correction` outranks a `hypothesis`, and it keeps outranking it: a record cannot change
its own authority after admission, and a superseded or corrected record stays readable with links
in both directions and a retained history
([docs/architecture.md §5](architecture.md#5-state-authority-and-uncertainty)). Repeated
compaction, resume or fork does not promote an obsolete intent into a current one — the original
user intent is resolved by derived id from the verbatim first prompt, never by relevance search,
and where the capture layer and the checkpoint disagree the capture layer wins and the
disagreement is logged loudly ([ADR 0011](adr/0011-rehydration-budget-and-item-order.md) §10).

**The recorded partial.** `plans/V5-report.md` §24 records the uncertainty gate as **partial**: it
"does not survive the digest surface under a blind ledger". Concretely (§29 item 11), a stale
record can render as `[active]` in a blind-ledger digest, and pin records are stamped `mcp` because
the ingest path has no production caller. Both are routed and unruled. Read an uncertainty marking
in a digest as a claim that has not been verified end to end.

## Additional context: budget and overflow

After a compaction, Qompack injects one block. Its size is capped and its order is fixed, per
[ADR 0011](adr/0011-rehydration-budget-and-item-order.md):

- **It always fits what Claude Code delivers whole.** Claude Code hands a hook's
  `additionalContext` to Claude only up to 10,000 characters; past that, Claude gets a file path and
  a 2,000-character preview instead. So the whole block — every heading, handle and the report on
  what was left out — is held to 9,500 characters, and no setting raises that. On a long session
  that is less than everything Qompack could restore, which is what section 7 and the retrieval
  tools are for.

- **The budget is a hard cap that is never raised.** A caller's budget may be lowered or filled in,
  never raised — not even to the configured minimum. Ask for 600 tokens and you get at most 600.
- **Order is importance, and importance decides what survives.** Items are filled in a fixed order;
  a payload that reordered them would be wrong even if it fit.
- **Truncation is by prefix, never cheapest-first.** Within an item, units are admitted in order
  and the first one that does not fit ends the item. So a smaller budget yields a prefix of what a
  larger one yields, which is why a cut is predictable.
- **"Never truncated" means never *partially* emitted.** Nothing is emitted half-rendered and
  nothing is emitted over the cap: an item that does not fit whole becomes a drop entry instead.
- **A path rule is restored whole or not at all.** A half-restored instruction is worse than an
  absent one, because you cannot tell you are reading half of it; an absent one at least appears in
  the drop report.
- **Overflow is explicit, never silent.** What did not fit is named in section 7 ("No longer in
  context") with the call that brings it back — `why(<decision id>)`, `re_read(<path>)`,
  `expand(tool_use_id=…)`, `already_tried(target="…", approach="…")` for an elimination, or `Read`
  on the rule file, the skill's `SKILL.md` or the checkpoint file — and, when the section
  cannot list everything, it ends in `… and N more; call dropped()`. The complete report is
  persisted regardless, which is what `/qompack:dropped` reads. An essential record that could not
  be carried whole marks the payload degraded.

The shipped defaults are in [docs/config-reference.md](config-reference.md#checkpoint) and its
`runtime` section: `checkpoint.budgetTokens` for the checkpoint artifact, `runtime.rehydrate.minTokens`
and `runtime.rehydrate.maxTokens` for the injected payload. The 9,500-character ceiling binds before
the default token budget does (it is roughly 2,400 tokens of prose), so raising `maxTokens` does not
make the block larger; lowering it below that can make it smaller
([docs/cannot-do.md](cannot-do.md#the-host-delivers-at-most-10000-characters-of-injected-context-whole)).

The rehydration budget is a target for Qompack-added material, not the total restored native context.
What the host restores on its own is the host's business; Qompack neither measures nor controls it,
and no claim here says the native input shrinks
([docs/architecture.md §7](architecture.md#7-checkpoint-and-rehydration)).

Injection has its own kill switch, `runtime.migration.reinjection.sessionStartCompact` (default
`true`). Setting it false stops injection without touching recording.

## Operator commands

These are run from a terminal rather than from a session. `qompack help` prints the same list.

**Which of them write.** `qompack status` creates `.qompack/` (index, logs, metrics, spool and the
rest of the layout) in the project directory it resolves, and starts that project's daemon. Running
it in a directory that has never been used with Qompack is therefore a write, in that directory —
verified by running the built binary in an empty scratch directory outside this repository.
`qompack self-test` does the same: when no daemon answers, its `daemon.reachable` check starts one
(`internal/cli/selftest.go`, `selfTestDaemonReachable`).
`qompack config print` and `qompack version` did not create `.qompack/` or start a daemon in the
same probe. The hook entry points (`checkpoint`, `flush`, `observe prompt|stop|tool`,
`session-start`) are invoked by Claude Code and always exit 0 — but exiting 0 is not the same as
doing nothing: `qompack checkpoint` run by hand with empty stdin created `.qompack/` and started
the daemon too, observed in a separate probe on this tree. Treat every hook entry point as a write.

Any subcommand accepts `--set <dotted.key>=<value>` to override configuration for that run.

| Command | What it does |
|---|---|
| `status` | the report described under [`/qompack:status`](#qompackstatus) |
| `config print [--provenance] [--json]` | the effective configuration; `--provenance` labels each leaf `default`, `user`, `project`, `env` or `flag` ([origins](config-reference.md#provenance-origins)) |
| `config schema` | the configuration JSON Schema — the machine-readable counterpart to [docs/config-reference.md](config-reference.md) |
| `self-test` | reports host-contract and subsystem checks as a table (or `{checks,mode,exit}` under `--json`); exits 1 for a critical failed check |
| `doctor [--project <root>] [--json]` | version, scope, per-capability evidence, disabled controls and gaps; read-only |
| `fsck [--project <root>] [--json] [--seal-check] [--repair] [--yes]` | store, index, checkpoint and backup integrity; read-only unless `--repair --yes`, which performs five explicit additive repairs and deletes nothing, or `--seal-check`, which also runs the full delivery-seal check and so takes the daemon lock |
| `version` | the plugin version |
| `admin delivery-seal [--project <root>] (--check \| --to v1) [--accept-torn-slot --yes]` | checks or converts the delivery journals' position seals; `--accept-torn-slot --yes` accepts a seal with one valid and one torn slot when the journal holds a complete tail past it. **The daemon must be stopped** |
| `backup create --project <root> --id <name> [--json]` | takes a consistent backup with the source daemon stopped |
| `backup verify --project <root> --id <name> [--json]` | validates the named backup's manifest and bytes |
| `backup restore --project <root> --id <name> --destination <fresh-project> [--json]` | restores into a fresh destination, proves same-build reads and runs integrity checks; source and later writes remain intact ([procedure](backup.md)) |
| `eval import` | imports recorded Claude Code transcripts as a redacted replay corpus |
| `daemon` | runs the resident per-project daemon in the foreground |
| `mcp` | runs the MCP server over stdio; the host starts this, you normally do not |
| `dropped`, `recall`, `pin`, `why`, `eval` | the slash commands' own binaries, described above |

**`doctor` and `fsck` are read-only diagnostics.** SP-17 implemented both. `qompack doctor` reports
version, scope, per-capability evidence, disabled controls and gaps; `qompack fsck` verifies store,
index, checkpoint and backup integrity across eighteen check rows. Both open the store read-only;
`fsck --repair` needs `--yes` and performs five explicit additive repairs that never delete anything
([docs/security.md §7](security.md#7-what-needs-an-operator-and-how-to-find-it),
[docs/release.md §5](release.md#5-rollback)). `fsck` exits 0 when clean, 1 on a defect, 2 on a
misuse.

**Not implemented in this build.** `bench` still prints `qompack bench: not implemented in this
build` and exits `1`: the help line advertises it and `internal/cli/commands.go` registers it in the
`notImplemented` list. `plans/V5-report.md` §29 item 7 carries `bench` as an open row to implement or
deprecate.

Reading `status` when nothing is running is normal: rows say `unavailable` with a reason. That is an
honest gap and not a failure. Two reasons recur — a budget with no per-hook instrument (its number
would have to be borrowed from an aggregate that mixes several hooks, and six identical figures
presented as six measurements would be six claims nobody made), and a budget nothing in this
repository can observe at all, such as the host's own process-creation cost
(`internal/commands/statuscollect.go`).

## Usage accounting

Three different numbers get called "cost", and Qompack keeps them apart
(`internal/eval/ledger.go`, `plans/V5-report.md` §25):

- **Subscription usage** is allowance-covered work — what your host shows you. It is not a
  per-token cash charge.
- **Estimated API price** is what Qompack computes from a configured rate schedule. It is an
  assumption, labelled as one, and it is a *lower bound* whenever a category's volume is unknown:
  unknown volumes are named, never priced at zero.
- **A reconciled invoice** is a separate observation from the provider. Qompack never derives one
  from an estimate, and it does not reconcile invoices at all.

Subscription usage and estimated API price are separate numbers; Qompack does not reconcile invoices.

The ledger records which of these a record came from (`subscription`, `estimate`, `invoice`, or
`none` for work no rate was applied to, such as a deterministic replay that made no model call) and
never merges them. `/qompack:eval` reports cost beside its verdict and never folds it in: a run
that was cheap and got the wrong answer must not pass. No rate is quoted anywhere in Qompack's
documentation, and `plans/V5-report.md` §25 records the estimated price for its own checkpoint as
`unknown`, with no rate table applied.

## Where to look next

- [docs/config-reference.md](config-reference.md) — every configuration key, generated; see also
  [Versioned blocks](config-reference.md#versioned-blocks),
  [Gated switches (ship off)](config-reference.md#gated-switches-ship-off) and
  [Retired-meaning keys](config-reference.md#retired-meaning-keys)
- [docs/commands.md](commands.md) — the slash commands and their exact flags, generated
- [docs/mcp-tools.md](mcp-tools.md) — the MCP tools and their argument schemas, generated
- [docs/troubleshooting.md](troubleshooting.md) — what each observation means and what to do
- [docs/cannot-do.md](cannot-do.md) — the capabilities this build does not have, and where each
  limit is recorded
- [docs/uat.md](uat.md) — the acceptance scenarios and the evidence a run has to leave
- [docs/architecture.md](architecture.md) — the contracts behind all of it
- [docs/adr/README.md](adr/README.md) — every architecture decision record, with its status

Packaging and release, added by SP-17:

- [docs/install.md](install.md) — installing, upgrading and uninstalling the bundle
- [docs/security.md](security.md) — the security and recovery posture
- [docs/release.md](release.md) — how a release is cut and what it claims to support

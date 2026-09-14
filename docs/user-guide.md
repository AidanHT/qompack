# User guide

This page explains what Qompack does in a session, what each command and tool reports, and how to
read the qualifications those reports carry. It does not restate the architecture — the contracts
live in [docs/architecture.md](architecture.md) — and it never restates a generated schema: the
command and tool pages are generated from the shipped code, so this page links them instead.

## Who this is for and what to expect

You are running a Claude Code session with the Qompack plugin installed, and you want to know what
it added, what it dropped, and how to get something back. (Installation and the packaged bundle are
planned (SP-17): docs/install.md and docs/security.md. Neither file exists yet, so neither is
linked here. Building from source is covered in [README.md](../README.md).)

**What it adds.** Qompack records what the session produces into `.qompack/` through the host's
hooks, writes a checkpoint at `PreCompact`, and — after the compaction — injects one bounded,
checkpoint-derived block through `SessionStart` with `source=compact`
([docs/architecture.md §7](architecture.md#7-checkpoint-and-rehydration)). Everything else it
offers is on demand: seven slash commands and eight MCP tools that read what was recorded.

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

The full list is planned (SP-18 Commit 5): docs/cannot-do.md.

**One warning before you run anything.** `qompack status` (and so `/qompack:status`) creates
`.qompack/` in the project directory it resolves and starts that project's daemon. It is a write.
See [Operator commands](#operator-commands) for exactly which commands do this.

## Slash commands

Qompack installs seven slash commands. Each shells out to the `qompack` binary, so
`/qompack:status` and `qompack status` are the same code. The inventory, every flag and the exact
help text are in [docs/commands.md](commands.md); this section says what each one is *for* and what
it can report.

Two things are true of all seven, per that page's preamble:

- `--json` emits a versioned envelope instead of text; `--help` prints the usage block.
- Exit codes are `0` success, `2` a malformed invocation, `1` anything else — except a hook entry
  point, which is the only kind of subcommand that always exits `0` (`internal/cli/dispatch.go`).

A third rule holds where a frontend's source of answers is left unbound rather than everywhere: it
reports that it is unavailable and exits `1` instead of inventing one. In this build that is
`/qompack:eval`, whose artifact seam is nil. It is not how `/qompack:checkpoint` behaves — see
below — because that name resolves to a hook, not to a frontend (`internal/commands`,
`internal/cli/qompack_commands.go`).

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

### `/qompack:checkpoint`

Installed, and **not yet routed** in this build. The command file ships and the host offers it —
`plugin/commands/checkpoint.md` runs `qompack checkpoint` — but that name is already the
`PreCompact` hook entry point (`internal/cli/hooks.go`, registered `Hook: true`), and no separate
local-seal route exists yet. `docs/commands.md` marks the subcommand column **not yet routed** for
exactly this reason, and `internal/cli/qompack_commands.go` declines to give the name a second
meaning: doing so needs the arch pre-step recorded as SP-14 handoff edge H3
(`plans/V5-report.md` §29 item 3).

So invoking it does not write a checkpoint on demand; it runs the hook. A hook reads a hook event
from stdin and **always exits `0`**, whatever happens inside it — `internal/cli/hooks.go` and the
exit-code policy in `internal/cli/dispatch.go` ("the ONLY code a hook subcommand may ever return"),
pinned by `internal/cli/qompack_commands_test.go`. Typed by hand, with no hook event arriving on
stdin, there is nothing for it to classify: it returns empty output and does not even create the
store (`internal/cli/hookclient.go`). Checkpoints are written at `PreCompact`, by the hook, from
the event the host supplies.

A frontend for a checkpoint-now command does exist (`internal/commands/cmd_checkpoint.go`, which
reports unavailable when its dependency is nil), but nothing routes to it in this build, so that is
not the behaviour you get from typing the command.

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

Runs the replay harness and reports the score against the baseline: `[--corpus <dir>]`
([schema](commands.md#qompackeval)).

The **baseline** is a named policy in the evaluation artifact — `eval.Report.Baseline`, "the Policy
every Regression is measured against" (`internal/eval/types.go`). The command reports the primary
policy's score beside it; it does not itself re-run the corpus, because running the harness is
`test/replay`'s job and a second driver would bring its own corpus selection
(`internal/commands/cmd_eval.go`).

What it can report: a verdict of `pass`, `fail`, or `inconclusive` — a distinct outcome for a run
whose trials were skipped, "because a gate that passes on an evaluation which did not run is not a
gate". A metric with no declared threshold is printed and explicitly **not judged**, and cost never
contributes to the verdict.

In this build the artifact seam is left unbound on purpose — "no committed convention for where a
completed evaluation's artifacts live" (`internal/cli/qompack_commands.go`) — so the command
reports that no evaluation artifacts are readable and exits `1`. `plans/V5-report.md` §29 item 3
records `Deps.EvalArtifacts` as open by design.

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
- **Overflow is explicit, never silent.** What did not fit is named in the drop report, the payload
  is marked degraded, and the complete report is persisted even when the rendered section was
  truncated to a counted line — which is what `/qompack:dropped` reads.

The shipped defaults that implement the target are in
[docs/config-reference.md](config-reference.md#checkpoint) and its `runtime` section:
`checkpoint.budgetTokens` for the checkpoint artifact, `runtime.rehydrate.minTokens` and
`runtime.rehydrate.maxTokens` for the injected payload.

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
`qompack config print` and `qompack version` did not create `.qompack/` or start a daemon in the
same probe. The hook entry points (`checkpoint`, `flush`, `observe prompt|stop|tool`,
`session-start`) are invoked by Claude Code and always exit 0.

Any subcommand accepts `--set <dotted.key>=<value>` to override configuration for that run.

| Command | What it does |
|---|---|
| `status` | the report described under [`/qompack:status`](#qompackstatus) |
| `config print [--provenance] [--json]` | the effective configuration; `--provenance` labels each leaf `default`, `user`, `project`, `env` or `flag` ([origins](config-reference.md#provenance-origins)) |
| `config schema` | the configuration JSON Schema — the machine-readable counterpart to [docs/config-reference.md](config-reference.md) |
| `self-test` | asserts every host contract and reports a table (or `{checks,mode,exit}` under `--json`). **The only command that may exit non-zero on a real finding** |
| `version` | the plugin version |
| `admin delivery-seal [--project <root>] (--check \| --to v1)` | checks or converts the delivery journals' position seals. **The daemon must be stopped** |
| `eval import` | imports recorded Claude Code transcripts as a redacted replay corpus |
| `daemon` | runs the resident per-project daemon in the foreground |
| `mcp` | runs the MCP server over stdio; the host starts this, you normally do not |
| `dropped`, `recall`, `pin`, `why`, `eval` | the slash commands' own binaries, described above |

**Not implemented in this build.** `doctor` and `fsck` each print `qompack <name>: not implemented
in this build` and exit `1` — they are SP-17 deliverables. `bench` does the same thing: the help
line still advertises it, and `internal/cli/commands.go` registers it alongside the other two, so
it is unavailable here as well. `plans/V5-report.md` §29 item 7 carries `bench` as an open row for
SP-17 to implement or deprecate.

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
- [docs/architecture.md](architecture.md) — the contracts behind all of it
- [docs/adr/README.md](adr/README.md) — every architecture decision record, with its status

Planned, and not yet written — named as plain text on purpose, because none of these files exists:

- planned (SP-18 Commit 4): docs/troubleshooting.md
- planned (SP-18 Commit 5): docs/cannot-do.md
- planned (SP-18 Commit 6): docs/uat.md
- planned (SP-17): docs/install.md, docs/security.md and docs/release.md

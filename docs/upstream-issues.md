# Upstream proposals

These are **proposals prepared by SP-18**. None of them has been filed. Nothing on this page has
been sent, submitted, drafted in a tracker, discussed with anyone, or acknowledged by anyone; the
page is text in this repository and nothing else. A proposal becomes an issue only under separate
authorization (`Qompack.md` v1.5 §12, "Upstream issues worth filing separately": "Sending issues
requires separate authorization"), and this page then records the issue URL beside the proposal it
came from.

Each proposal names a host limitation as observed from this repository, the evidence that Qompack
cannot assert the thing today, the smallest host change that would let it, and what Qompack would do
with that change. The evidence is the point: a proposal without a named source is a wish. Where the
limitation is stated as "not exposed to a plugin", that is a statement about what this repository
could find and use — an absence of a documented surface, never a claim about Claude Code's internals.
`plans/QOMPACK-ERRATA.md` v1.3, "What could not be verified, and will not be", is the standing rule
for that distinction: of the host compaction internals named by identifier in the planning document,
"None appears in published documentation. None can be confirmed or refuted from it" — so they are
treated as motivating background at the version they were written against, not as a live contract,
and nothing on this page rests on one.

Every one of these limits is recorded on [docs/cannot-do.md](cannot-do.md) as a limit Qompack lives
within today. None of them is a blocker: Qompack is designed to degrade rather than to depend on a
capability it cannot verify.

| # | Proposal | Status |
|---|---|---|
| 1 | Observability of delivered `additionalContext` | `not filed` |
| 2 | A PreCompact output channel for summarizer focus, and time to write | `not filed` |
| 3 | A post-compaction signal | `not filed` |
| 4 | Per-hook latency attribution | `not filed` |
| 5 | MCP server registration visibility | `not filed` |
| 6 | Usage telemetry per request category | `not filed` |
| 7 | A supported compaction request/veto or marker API | `not filed` |

**Residuals accepted for 0.3.0, and which of them a host change could lift.** The V6 close-out
accepted a set of known limits by recorded decision (`plans/V6-CLOSEOUT-CHECKLIST.md`); each is on
[docs/cannot-do.md](cannot-do.md) or in [docs/release.md](release.md#capability-status-at-030)'s
capability table. Most are product limits, which no host change would lift, so no proposal is
prepared for them here:

| Residual | Decision | Host side |
|---|---|---|
| A rotation's capture pause, and the halt of store GC past the carried-lease bound | D6, D16 | none: product-side |
| A compaction at the edge of session-start's budget gets the deferred note | D29 | none prepared |
| A prompt captured out of host order (live-versus-spool race, hook pid reuse) | D35(b), D38 | none: product-side |
| A `SessionEnd` that arrives while the daemon is stopping waits for the next session | D35(c) | partly: plugin `SessionEnd` hooks share one 1.5 s host budget; no proposal prepared |
| Spool submode does not switch back within a session | D44 | none: product-side |
| A page beside interleaved redacted regions is shorter than it could be | D48 | none: product-side |
| PutBytes (SP06-D2) and the 256 KB `OnToolUse` row (SP08-D1) miss their budgets | D54 | none: product-side |
| A `PreCompact` waits behind a client-spool watcher pass already running | D56(e) | none: product-side |
| Read rules that exist only in the running session are invisible to a plugin | D7 | yes: no interface lets a plugin ask whether a native Read would be allowed; no proposal prepared |
| Host behaviours: the host hands a hook a tool's content whole or not at all, so a non-exact capture cannot arise live on 2.1.280 (UAT-02); it re-attaches files after a compaction and decodes binary files itself; usage categories are not exposed | D45, D49 | proposal 6 for usage categories; none prepared for the others |

## 1. Observability of delivered `additionalContext`

- **Host limitation.** A plugin that returns `additionalContext` from a SessionStart hook with
  `source=compact` receives no signal about what happened to it: whether it was delivered to the
  model at all, and how much of it. No documented host surface reports this.
- **Evidence.** `hook.additional_context_delivered` — the assertion whose job this is — has no
  recorded observation. In `qompack self-test` it reports `not-yet-implemented`, the producer-absent
  state, because self-test runs the standard assertions against a zero `daemon.Services`
  (`internal/cli/selftest.go` `selfTestContractAssertions`) and the producer is declared only when
  the rehydrate seam is bound (`internal/daemon/options.go` `DeclareProducers`); the gated wrapper
  then never runs the real check at all (`internal/contract/assertions.go`;
  `internal/contract/ids.go` `CAdditionalContext`). What a live daemon's run would observe against
  an installed host has never been recorded: installed-host verification is claimed nowhere
  (`plans/V5-report.md` §24, B01; `plans/MIGRATION-EVIDENCE.md` "Capability register inputs"). The design it belongs to,
  [ADR 0011](adr/0011-rehydration-budget-and-item-order.md), can bound and order what Qompack emits
  but cannot observe what the host did with it, and `internal/contract/capability.go` records
  injection as `implemented_unverified` with the note that an observed sentinel "documents one
  delivery under the tested contract, never complete context or model compliance". One part of
  "how much of it" is now documented and observed: over 10,000 characters the host swaps the text
  for a file path and a 2,000-character preview, with no signal to the hook
  ([docs/cannot-do.md](cannot-do.md#the-host-delivers-at-most-10000-characters-of-injected-context-whole)).
  Qompack now holds its compact rehydration to 9,500 characters so that case does not arise for it;
  a delivered-size signal would still be the only way to confirm it on a given host.
- **Proposal.** Report delivery of hook-supplied additional context back to the hook's own process:
  a delivered/not-delivered flag and the delivered size, on the same event, would be enough.
- **What Qompack would do with it.** Declare the producer for
  `hook.additional_context_delivered`, so the assertion observes for real instead of reporting the
  producer-absent state, and drop "no exact native loaded bytes" from
  [docs/cannot-do.md](cannot-do.md) for the part that concerns Qompack's own injected block. It
  would not lift the model-compliance limit, which is a different question.
- **Status: not filed.**

## 2. A PreCompact output channel for summarizer focus, and time to write

- **Host limitation.** A PreCompact hook cannot contribute anything to the summary. The hooks
  reference (fetched 2026-09-22) documents PreCompact decision control as top-level
  `decision: "block"`/`reason` only, with no `hookSpecificOutput` variant, and "Claude Code discards
  a PreCompact hook's `systemMessage` and `continue` fields"; `custom_instructions` is PreCompact
  *input* — what the user typed after `/compact`, `null` for auto-compaction. Separately, how much
  wall time the hook has before compaction proceeds without it is undocumented as a guarantee.
- **Evidence.** Claude Code 2.1.280 rejected the focus instruction Qompack returned as
  `hookSpecificOutput.customInstructions`: "Hook JSON output validation failed —
  hookSpecificOutput.hookEventName: expected one of "PreToolUse" | "UserPromptSubmit" | …", and it
  appended the whole rejection, instruction text included, to the post-compaction transcript
  ([evidence](../plans/sdd/V6-closeout/packaging/evidence/c1.12-host-rejection.txt)). Qompack now
  answers PreCompact with the empty object and has retired the instruction (C1.18). `precompact.has_time_to_write` ("measured PreCompact
  wall time vs. the manifest timeout") still has no installed-host observation recorded (B01).
- **Proposal.** Give PreCompact a `hookSpecificOutput` that appends plugin-supplied focus text to
  the summarization request (the way the user's own `/compact <instructions>` does), and state the
  PreCompact timeout as a contract.
- **What Qompack would do with it.** Deliver the checkpoint's span paragraph ("the checkpoint
  covers the session through turn N; summarize only what came after"), which `internal/checkpoint`
  can still compose but the daemon, since C1.18, no longer returns or records, and size its
  PreCompact work against a stated budget instead of a conservative guess. It would still not be a
  summarizer setter.
- **Status: not filed.**

## 3. A post-compaction signal that does not have to be inferred

- **Host limitation.** No post-compaction event is used by this plugin, and none is contractual
  here. `Qompack.md` v1.5 §7.3 records PostCompact among the surfaces current documentation
  describes, with "installed support remains unverified" — so this page makes no claim either that
  such an event is delivered to a plugin or that it is not. What Qompack does is infer the boundary
  from the next SessionStart carrying `source=compact`, because a capability may not depend on an
  event whose delivery it cannot verify.
- **Evidence.** `internal/contract/capability.go` records the design consequence directly:
  "Reinjection has no PostCompact prerequisite: it cannot wait for an optional event." The pairing
  Qompack infers instead is its own assertion, `session_start.source_compact` — "after a PreCompact
  is observed, the next SessionStart must arrive with source == \"compact\" within the same session
  id. Recorded in `state/contract.json` and evaluated on the FOLLOWING start"
  (`internal/contract/ids.go`). `Qompack.md` v1.5 §7.3 lists PostCompact among the surfaces current
  documentation describes while "installed support remains unverified".
- **Proposal.** Make a post-compaction signal contractual and observable to a plugin — a documented
  one-shot event carrying the session id and the compaction's outcome, with a stated guarantee about
  when it is delivered — so a plugin need not reconstruct the boundary from the event that follows
  it, and need not treat the signal as optional.
- **What Qompack would do with it.** Observe the compaction boundary directly instead of evaluating
  it one session-start late, which would remove a class of missed pairings when a session ends
  between the two events. Qompack would keep working without it: the reinjection path must not
  acquire a dependency on an optional event, and would not.
- **Status: not filed.**

## 4. Per-hook latency attribution

- **Host limitation.** A hook process cannot attribute its own end-to-end latency per hook event.
  Nothing tells the created process what the host measured for it, and the process cannot observe
  the part that happens before it starts.
- **Evidence.** `qompack status` prints `unavailable` for every per-hook row with the reason
  `internal/commands/statuscollect.go` gives it: "no per-hook instrument: PostToolUse is folded into
  the hook_controlled aggregate, which mixes every delivering hook and cannot be attributed to one"
  (`hookRows`, formatted from the `no per-hook instrument: %s is folded into the %s aggregate`
  string). The neighbouring B-D row says the other half: "no instrument records this histogram in
  this build; it measures host process creation, which the created process cannot observe". Both are
  quoted in [docs/troubleshooting.md §2](troubleshooting.md#2-unknown-capability-or-telemetry).
- **Proposal.** Expose per-hook timing to the hook's own process — at minimum the host-measured
  duration of the invocation, keyed by hook event — instead of leaving a plugin to fold every
  delivering hook into one aggregate.
- **What Qompack would do with it.** Fill the per-hook latency rows with a measured number instead
  of the availability word, and judge the per-event budgets separately. It would not change
  [ADR 0010](adr/0010-wall-clock-under-coload.md)'s rule that a wall-clock figure is judged only
  where it is judgeable.
- **Status: not filed.**

## 5. MCP server registration visibility

- **Host limitation.** A plugin cannot tell whether the host registered the MCP server its
  `.mcp.json` declares. The stdio server learns it exists only if a client speaks to it, and a host
  that never launched it produces the same silence as a host that launched it and sent nothing.
- **Evidence.** `mcp.server_registered` — "the MCP server received initialize at least once this
  session" (`internal/contract/ids.go` `CMCPRegistered`) — reports `not-yet-implemented` in
  `qompack self-test`, which runs it against a zero `daemon.Services`; the producer is declared only
  where the daemon binds `s.MCPInitialized` (`internal/daemon/mcpop.go`), and no installed-host run
  has recorded what that assertion observes (B01). `internal/mcp`'s package comment records the related asymmetry: Claude Code "launches an MCP
  server once per client and hands it no session_id", which is why the daemon, not the stdio
  process, resolves the session.
- **Proposal.** Make registration observable to the plugin that declared the server: report whether
  the declared server was registered, and if it was refused, why.
- **What Qompack would do with it.** Declare the producer for `mcp.server_registered` and answer
  "are the retrieval tools actually available in this session?" in `qompack self-test`, instead of
  reporting the producer-absent state. Today the honest answer is that the row asserts nothing —
  [docs/troubleshooting.md §1](troubleshooting.md#1-start-with-provenance).
- **Status: not filed.**

## 6. Usage telemetry per request category

- **Host limitation.** Per-request usage categories — cache read, cache write, input, output — are
  not exposed to a plugin, and neither are TTL or retry counts. A plugin that wants to account for
  usage can parse only what a transcript happens to carry.
- **Evidence.** `plans/V5-report.md` §25 records it as measured absence: "Cache categories, TTL,
  retries and the coordinator's own usage are not exposed to this session: `unknown`. Estimated
  price: `unknown` (no rate table applied; subscription allowance is not cash per token)."
  `plans/MIGRATION-EVIDENCE.md` records what Qompack can parse today: usage attribution "parses
  exactly one field (`output_tokens`, `internal/eval/importer.go:55-57`) and is otherwise absent".
  `Qompack.md` v1.5 Appendix A scopes the quantity itself: "Request price — sum reported category ×
  applicable dated rate, plus non-token charges; unknown stays unknown", and §5's cache arithmetic
  is a break-even condition, not a price.
- **Proposal.** Expose per-request usage by category to plugins, with the cache TTL that applied, so
  an accounting plugin reports categories it was given rather than inferring them.
- **What Qompack would do with it.** Record the categories in its own ledger and stop writing
  `unknown` where a count exists. It would still not publish a price: a rate table and a
  subscription allowance are outside this repository, and an estimate is never written as an invoice
  ([docs/cannot-do.md](cannot-do.md), "No cost or price guarantee").
- **Status: not filed.**

## 7. A supported native compaction request/veto or marker API

- **Host limitation.** There is no supported way for a plugin to request a compaction, to veto one,
  or to control cache markers or native history.
- **Evidence.** `Qompack.md` v1.5 §12 states the design consequence as a requirement on Qompack, not
  as a complaint about the host: "The production plugin must function without native compaction
  request/veto." `plans/MIGRATION-EVIDENCE.md` records history rewriting and native eviction as
  `unsupported` and "Excluded", with "no mechanism" for compaction requests, compaction blocking and
  history rewriting in the tree survey. The corresponding switch is refused on purpose:
  `runtime.migration.compaction.automaticVeto` stays `false` because "the recovery/proactive
  distinction is unverified"
  ([docs/config-reference.md](config-reference.md#gated-switches-ship-off)), and
  `runtime.migration.compaction.blockManualCompact` is hardwired `false` so a manual `/compact` is
  never blocked.
- **Proposal.** If such a control is ever offered, offer it with the distinction Qompack would need
  to use it safely: which compaction is a recovery and which is proactive. A veto without that
  distinction is not usable.
- **What Qompack would do with it.** Nothing, until the distinction is verified in a target host.
  Qompack does not depend on this capability and would keep the switch gated off; the entry exists
  so the requirement is on record, not because a gate is waiting on it. A manual `/compact` would
  remain unblockable regardless: that is a hardwired local rule, not a host limitation.
- **Status: not filed.**

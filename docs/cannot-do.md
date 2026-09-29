# What Qompack cannot do

This page is the long form of [docs/architecture.md §10](architecture.md#10-what-is-not-supported).
Its binding source is `Qompack.md` v1.5 §12, "What this plugin cannot do"; every entry below names
the file and section it is recorded at, and says what Qompack does instead of the thing it cannot
do.

Read it as a capability boundary, not as a to-do list. Some of these limits are host boundaries a
plugin cannot cross at all; some are gates this build has not passed; the **Recorded at** line tells
you which, and the last group ("Not in this build") is the one that can move.

Two pages carry the rest of the same material and are not repeated here:
[docs/troubleshooting.md §10](troubleshooting.md#10-what-not-to-conclude) lists what a given output
does *not* prove, and [docs/upstream-issues.md](upstream-issues.md) states which of these limits a
host change could lift — as prepared proposals, none of which has been filed.

## 1. Native context control

### No native-history cuts, and no marker control

- **Limit.** Qompack cannot perform native-history cuts, and it has no marker control: it cannot
  move, insert or delete Claude Code's own cache markers or rewrite the native conversation.
- **Why.** No documented host surface exposes either one. The capability register records history
  rewriting and native eviction as `unsupported`, and the code survey behind it found no mechanism
  at all — "two in-code statements say a plugin cannot move native cache markers".
- **What Qompack does instead.** It records alongside the host: it captures tool results and file
  versions into its own store, and after a compaction it offers material back through
  `additionalContext`. What the host keeps or drops stays the host's decision.
- **Recorded at.** `Qompack.md` v1.5 §12 "What this plugin cannot do"; `plans/MIGRATION-EVIDENCE.md`
  "Capability register inputs" and its capability table row *History rewriting / native eviction —
  unsupported — Excluded*; [docs/architecture.md §10](architecture.md#10-what-is-not-supported).

### No deletion of already-delivered results

- **Limit.** Qompack cannot cause the deletion of already-delivered results. Once the host has
  delivered a tool result into the conversation, nothing here removes it.
- **Why.** The host boundary rule admits observation, reinjection and replacement of a *newly*
  delivered result only, "through documented and target-tested adapters"; deletion of delivered
  results is named on the excluded side of that sentence. New-result replacement itself is off on
  this tree (`runtime.migration.replacement.newResult`, refused until the SP-21 M4 gate passes).
- **What Qompack does instead.** It stores what it observed so the content can be retrieved later
  ([docs/mcp-tools.md](mcp-tools.md)), and it reports what its own injected payload dropped
  (`/qompack:dropped`). Neither touches what the host already delivered.
- **Recorded at.** `Qompack.md` v1.5 §12; `plans/MIGRATION-EVIDENCE.md` item 7 "Host boundary";
  [docs/config-reference.md](config-reference.md#gated-switches-ship-off) "Gated switches (ship
  off)".

### No history eviction, and an `ephemeral` tag is a Qompack record property

- **Limit.** Qompack cannot evict anything from native context, and the `ephemeral` metadata on a
  retrieval response is not a host eviction control.
- **Why.** `internal/mcp`'s package comment states it directly: "Retrieval metadata marks Qompack
  representations ephemeral; it does not control host eviction or establish native context
  retention." The capability register calls ephemeral metadata "a future representation hint only".
- **What Qompack does instead.** It marks its own representations, so a client can tell a Qompack
  record from a host one. Coverage values (`archive_only`, `native_load_observed`, `unknown`) say
  what was observed about a record's whereabouts and never describe complete native history —
  [docs/user-guide.md](user-guide.md#fidelity-coverage-and-error-states).
- **Recorded at.** `internal/mcp` package comment; [docs/mcp-tools.md](mcp-tools.md) intro
  ("Ephemeral metadata describes Qompack records"); `plans/MIGRATION-EVIDENCE.md` capability table.

### No native compaction request/veto

- **Limit.** Qompack cannot request a native compaction and cannot veto one. The production plugin
  must function without a compaction request/veto, and does.
- **Why.** §12 requires it, and the switch that would carry a veto is refused:
  `runtime.migration.compaction.automaticVeto` defaults `false` and is refused because "the
  recovery/proactive distinction is unverified". `runtime.migration.compaction.blockManualCompact`
  is hardwired `false` — `internal/config/validate.go` raises a violation on `true`, with the
  message "must be false: a manual compact is never blocked for optimization" — so a manual
  `/compact` is never blocked; the key exists only to say so.
- **What Qompack does instead.** It treats compaction as an event to survive rather than an event to
  drive: PreCompact writes a checkpoint, and the next SessionStart with `source=compact` offers a
  bounded payload back. Scheduler pacing is advisory: `scheduler.youngDaly.enabled` and
  `scheduler.idle.deepCutWhenCold` are retired-meaning keys — "no native compaction trigger, cut or
  veto depends on it", and "no native cut is available to a plugin".
- **Recorded at.** `Qompack.md` v1.5 §12; `internal/config/validate.go` (the hardwired rule);
  [docs/config-reference.md](config-reference.md#gated-switches-ship-off) and
  [its retired-meaning keys](config-reference.md#retired-meaning-keys).

## 2. Proof

### No proof of model compliance with injected material

- **Limit.** Qompack cannot prove model compliance: nothing here observes whether the model read,
  used or obeyed material Qompack injected.
- **Why.** The injection capability is recorded `implemented_unverified`, and its own note sets the
  ceiling: "An observed sentinel documents one delivery under the tested contract, never complete
  context or model compliance." A sentinel is evidence of a delivery, not of a behaviour.
- **What Qompack does instead.** It reports what it emitted and what it had to drop, and marks a
  truncated payload degraded ([docs/user-guide.md](user-guide.md#additional-context-budget-and-overflow)).
- **Recorded at.** `Qompack.md` v1.5 §12; `internal/contract/capability.go` (`CapInjection` notes);
  [docs/architecture.md §10](architecture.md#10-what-is-not-supported).

### No exact native loaded bytes

- **Limit.** Qompack cannot report exact native loaded bytes — how much of what it injected reached
  the model, or how many bytes the host holds.
- **Why.** The assertion that would observe a delivery, `hook.additional_context_delivered`, has
  produced no observation anywhere you can read one. `qompack self-test` runs the standard
  assertions against a zero `daemon.Services` (`internal/cli/selftest.go`,
  `selfTestContractAssertions`), so that assertion's producer is undeclared *there* and the row
  reports `ok / info / not-yet-implemented` — the producer-absent state, which means no assertion
  was made at all. A live daemon does declare the producer once its rehydrate seam is bound
  (`internal/daemon/options.go` `DeclareProducers`, guarded by `s.Rehydrate`), but no run against an
  installed Claude Code has ever been recorded here (B01), so there is no observed delivery figure
  from that path either. A number nobody measured is `unknown`, not zero.
- **What Qompack does instead.** It counts its own emitted tokens against its own budget and prints
  the availability word rather than a borrowed number when there is no measurement —
  [docs/troubleshooting.md §1](troubleshooting.md#1-start-with-provenance) and
  [§2](troubleshooting.md#2-unknown-capability-or-telemetry).
- **Recorded at.** `Qompack.md` v1.5 §12; `internal/contract/ids.go` (`CAdditionalContext`);
  `internal/contract/assertions.go` (`gated`); proposal 1 in
  [docs/upstream-issues.md](upstream-issues.md).

### No summarizer model substitution

- **Limit.** Qompack cannot perform summarizer model substitution: it cannot choose, replace or
  configure the model that writes the host's compaction summary, and it cannot hand the summarizer
  any instruction at all. A plugin's PreCompact hook has no output channel for one.
- **Why.** `Qompack.md` v1.5 §7.3 states the boundary: "`custom_instructions` is PreCompact input,
  not a summarizer-output setter." The hooks reference (fetched 2026-09-22) gives PreCompact only
  top-level `decision: "block"`/`reason` and no `hookSpecificOutput` variant, and "Claude Code
  discards a PreCompact hook's `systemMessage` and `continue` fields"; `custom_instructions` is what
  the *user* typed after `/compact`. This was observed, not only read: Claude Code 2.1.280 rejected
  the focus instruction Qompack used to return ("Hook JSON output validation failed —
  hookSpecificOutput.hookEventName: expected one of …") and replayed the rejection into the
  post-compaction context (C1.12,
  [evidence](../plans/sdd/V6-closeout/packaging/evidence/c1.12-host-rejection.txt)). The only
  documented ways to steer the summary are `/compact <instructions>`, typed by the user, and a
  `# Compact instructions` section in the project's `CLAUDE.md`, which Qompack does not write. The
  key that once implied otherwise, `checkpoint.incrementalSpanInstruction`, is retired-meaning and
  read for compatibility only.
- **What Qompack does instead.** It writes its own checkpoint at PreCompact, which is Qompack's
  artifact and does not depend on what the summarizer produces, and it answers the PreCompact hook
  with the empty object — the only response the host accepts from it. The focus instruction is
  retired (C1.18): the daemon neither returns nor records one, and
  `precompact.custom_instructions_accepted` reports `retired`, attributed to an unsupported
  capability, instead of probing the transcript for text the host never received. The hook client
  still strips any PreCompact output an older, still-resident daemon might send
  (`internal/hookio` `ConformOutput`).
- **Recorded at.** `Qompack.md` v1.5 §12, §7.3 and §8.5 ("Retire O1's output setter");
  [docs/config-reference.md](config-reference.md#retired-meaning-keys);
  `testdata/host/hooks-output-schema.json`; proposal 2 in
  [docs/upstream-issues.md](upstream-issues.md).

### The rehydration budget is Qompack-added material, not restored native context

- **Limit.** The rehydration budget bounds what Qompack adds. It is not a measurement or a promise
  about the native context the host restores, and no claim here says the native input shrinks.
- **Why.** [ADR 0011 §3](adr/0011-rehydration-budget-and-item-order.md) makes `Request.Budget` a
  hard cap that is never raised; it governs the payload Qompack renders, and nothing else. What the
  host restores on its own is not observed here.
- **What Qompack does instead.** It fills a fixed item order inside the cap, truncates by prefix,
  and names what did not fit in the drop report —
  [docs/user-guide.md](user-guide.md#additional-context-budget-and-overflow).
- **Recorded at.** [ADR 0011](adr/0011-rehydration-budget-and-item-order.md) §3 and §4;
  [docs/architecture.md §7](architecture.md#7-checkpoint-and-rehydration).

### The host delivers at most 10,000 characters of injected context whole

- **Limit.** A hook field longer than 10,000 characters does not reach Claude whole: the host keeps
  the full text in a file and gives Claude its path and a preview of the first 2,000 characters.
  Qompack cannot raise the cap, so a rehydration can carry at most that much — less than a long
  session's restorable material (a single restored rule can run to thousands of characters), so some
  of it is always left for Claude to fetch.
- **Why.** The hooks reference (fetched 2026-09-22): "A hook's `additionalContext`,
  `systemMessage`, and `initialUserMessage` strings, and its plain stdout, are capped at 10,000
  characters"; over it "Claude Code saves the output to a file in the session directory and
  replaces it with the file path and a preview of up to the first 2,000 characters", "this cap has
  no setting or environment variable to raise it", and "Claude Code doesn't ask Claude to read the
  file". Observed on Claude Code 2.1.280: an 11,082-character SessionStart context reached Claude as
  a 2,391-character `<persisted-output>` block, and the model could quote only what the preview held
  ([evidence](../plans/sdd/V6-closeout/packaging/evidence/review/f2-live-host-cap-probe/README.txt)).
  The host accepts such a response, so no check fails. The unit is UTF-16 code units: the host's own
  code tests `field.length <= 1e4`
  ([evidence](../plans/sdd/V6-closeout/rehydrate-cap/evidence/host-cap-unit.txt)).
- **What Qompack does instead.** It fits the cap (owner decision D5). The whole compact
  `additionalContext`, contract probe included, is at most 9,500 host characters. Records are chosen
  in the fixed §8.6 order and admitted whole or not at all; each one left out is named in section 7
  of the payload with the call that brings it back (`why`, `re_read`, `expand`, `already_tried` with
  the elimination's own target and approach, or `Read` on the rule, skill or checkpoint file), and the
  section ends in a counted tail pointing at `dropped()` when it cannot list everything. The hook client still records a Loud line if any field ever overruns the cap
  (`internal/hookio` `HostCapOverruns`); for the rehydration that is a defence that does not fire.
- **Recorded at.** `testdata/host/hooks-output-schema.json` (`limits`);
  [ADR 0011 §21](adr/0011-rehydration-budget-and-item-order.md);
  `internal/rehydrate` `TestBuild_NeverExceedsTheHostCeiling`; `test/e2e`
  `TestE2E_SessionStartCompactFitsTheHostCap`; `internal/cli` `TestHookOutput_OverTheHostCapIsLoud`.

### No PostCompact dependency

- **Limit.** Qompack does not depend on a post-compaction event. There is no PostCompact
  prerequisite anywhere in the reinjection path, and this page makes no claim that such an event is
  delivered to a plugin.
- **Why.** The capability record is explicit: "Reinjection has no PostCompact prerequisite: it
  cannot wait for an optional event." A capability that waits on an optional host event fails
  silently when the event does not come.
- **What Qompack does instead.** It infers the boundary from the next SessionStart carrying
  `source=compact`, which is the tested adapter, and records the pairing as the
  `session_start.source_compact` assertion.
- **Recorded at.** `internal/contract/capability.go` (`CapInjection` notes);
  [docs/architecture.md](architecture.md#7-checkpoint-and-rehydration); proposal 3 in
  [docs/upstream-issues.md](upstream-issues.md).

## 3. Reconstruction

### It cannot reconstruct uncaptured history from current files

- **Limit.** Qompack cannot reconstruct uncaptured history by reading the files that exist now. If a
  turn was not observed, it is not recoverable from the working tree.
- **Why.** Current file content is a statement about the present, not evidence about the past;
  substituting it would manufacture history that nobody recorded. The architecture forbids the
  pointer that would imply otherwise: a capture failure "leaves the host result in place and forbids
  any handle or pointer that claims it recoverable" (`plans/00-ARCHITECTURE.md` §0.2.2).
- **What Qompack does instead.** It separates historical reads from current reads: `re_read` returns
  the store's own version history and is "never a live read of disk"
  ([docs/mcp-tools.md](mcp-tools.md)), and a record with no envelope carries `unknown` fidelity
  rather than `exact`.
- **Recorded at.** `Qompack.md` v1.5 §12; `plans/00-ARCHITECTURE.md` §0.2.2;
  [docs/user-guide.md](user-guide.md#current-vs-historical-reads).

### A missing or `redacted`/`truncated`/`binary` original stays that way

- **Limit.** Qompack cannot restore an original it does not hold. A record whose fidelity is
  `redacted`, `truncated`, `binary`, `partial`, `failure` or `unknown` is returned as it is, with
  that label; no substitute is invented and no gap is filled from elsewhere.
- **Why.** Each label names something that happened at capture time: privacy policy removed content,
  a size bound cut it, the bytes were not text, or the capture failed outright. The label is the
  honest answer; replacing it with a plausible reconstruction would erase the one signal telling you
  not to trust the bytes.
- **What Qompack does instead.** It returns the label with the content and separates it from the
  outcome enumeration, so `denied` (withheld) and `unavailable` (lookup failed) never read as
  "absent" — [docs/user-guide.md](user-guide.md#fidelity-coverage-and-error-states) and
  [docs/troubleshooting.md §4](troubleshooting.md#4-denied-or-unavailable-evidence).
- **Recorded at.** `internal/core/evidence.go` (the three enumerations);
  [ADR 0013](adr/0013-migration-contracts.md) ("errors read as absence" is a named defect).

### No complete capture of unobserved child work

- **Limit.** Qompack cannot promise complete capture of unobserved child work. Work done inside a
  subagent or child process that produces no hook event Qompack sees is not in the store.
- **Why.** Capture is driven by the hook surface — the seven events declared in
  `internal/pluginmanifest/manifest.go` — so anything that surface does not deliver is not observed.
  Coverage of what a child did is therefore qualified, never complete.
- **What Qompack does instead.** It reports coverage as a value rather than as an assumption
  (`unknown` is a real answer), and `/qompack:dropped` is documented as "Qompack's recorded omissions
  for this session", which "does not establish what remains in native context".
- **Recorded at.** `Qompack.md` v1.5 §12 and §7.3 (hook surface);
  [docs/mcp-tools.md](mcp-tools.md#dropped);
  [docs/troubleshooting.md §3](troubleshooting.md#3-capture-gaps).

## 4. Guarantees Qompack does not make

### No universal improvement and no universal savings

- **Limit.** Qompack claims no universal improvement and no universal savings. There is no claim
  that any session gets better, cheaper or shorter, on average or at all.
- **Why.** Nothing has measured it. `plans/V5-report.md` §26 records that no held-out task runs,
  stochastic baselines or quality trials were executed, and that SP-21's quality row is
  "inconclusive by construction". An unmeasured effect is not a small effect; it is an unknown one.
- **What Qompack does instead.** It reports executed test results and single-machine benchmarks with
  their conditions stated, and records inconclusive outcomes as inconclusive.
- **Recorded at.** `Qompack.md` v1.5 §12; `plans/V5-report.md` §24 (the SP-21 enabled-surface matrix)
  and §26; [docs/architecture.md §10](architecture.md#10-what-is-not-supported).

### The first captured prompt is not always the first prompt the host sent

- **Limit.** Qompack cannot promise that `prompt_<session>_0`, which rehydration item 2 injects as the
  verbatim original intent, is the prompt the host sent first. A prompt's turn is the order it was
  captured in. Prompts that reach the daemon live are captured in arrival order, and prompts
  replayed from the hooks' client spools (the HotSpool submode, `runtime.daemon.enabled=false`, a
  hook that could not reach the daemon) are replayed in the order their hooks stamped them. Two
  cases fall outside both. In the first, a prompt's hook could not reach the daemon and spooled it,
  then a later prompt arrived live and was captured before a drain replayed the spool. In the
  second, a spool file is named by the hook's pid, so a later hook that reuses the pid appends to an
  earlier hook's file. One drain pass replays a file's records in file order, so that later prompt
  can be replayed ahead of another file's earlier one. Either way the later prompt can take turn 0.
- **Why.** The daemon cannot see a prompt that exists only in a hook's spool, so it cannot hold the
  live prompt back for it without waiting on a file that may never appear. Replay is ordered file by
  file, and a file shared by two hooks through pid reuse keeps its own record order. Captured turns
  are never renumbered, because every later artifact is numbered against them. The V6 close-out's
  owner decision D35 ruled the live race out of the host-order guarantee and specified the
  file-by-file order.
- **What Qompack does instead.** Every prompt record carries the host's timestamp. Any capture that
  lands behind a turn its host sent later, from either source, is counted
  (`observer.prompt_out_of_host_order`) and logged as a Warn naming the turn it came in behind. A
  rehydration whose turn 0 is not the session's earliest-stamped prompt says so. Section 7 carries
  a `user_intent_source` entry, `host_order`, naming both records and the `expand(tool_use_id=…)`
  call for the host-first one. The client-spool watcher limits the live race's window: once the
  daemon serves another request, a spooled prompt is replayed within about two check intervals of
  2 s each, so only a live prompt sent inside that window can come in ahead of it. A spool file
  that a pid-reusing hook appended to after a drain had begun it is placed by the record the next
  drain replays from it, so reuse reorders prompts only when both hooks spooled before one pass.
- **Recorded at.** `plans/V2-SP-08-carried-defects.md` (SP08-D3, with the D35 close-out note);
  `plans/CARRIED-DEFECTS.tsv`; [docs/architecture.md §7](architecture.md#7-checkpoint-and-rehydration).

### A forked session's parent is inferred, not reported by the host

- **Limit.** `claude --resume <id> --fork-session` starts a new session that continues the parent's
  task, so Qompack keeps the parent's original request as the fork's original intent and treats the
  fork's own prompts, its first one included, as later statements of that task. No hook names the
  parent, though. Qompack takes the project's newest checkpoint when the fork starts as the one it
  continues. If you fork an older session after a newer one has compacted in the same project, the
  fork inherits the newer session's intent. If the project has no checkpoint yet, the fork's parent
  is unknown and its own first prompt stands as its original request.
- **Why.** Claude Code reports a fork only as `SessionStart` with `source` `fork` and the new
  session id. Nothing in any hook payload identifies the session it was forked from.
- **What Qompack does instead.** It records the inference once, when the fork starts, in
  `.qompack/state/lineage-<session>.json`, and never re-points it. The rehydration labels the
  inherited original with the session it came from (`(forked session: the original request of session
  …)`), checks it against that session's own verbatim capture, and adds a `user_intent_source` entry
  with id `fork` to section 7, naming the parent and the `expand(tool_use_id=…)` call for its first
  prompt. A fork with no known parent gets the same entry, saying its parent is unknown.
- **Recorded at.** `plans/sdd/V6-closeout/live/report.md` (F-UAT06-1);
  `internal/checkpoint/lineage.go`; [docs/architecture.md §7](architecture.md#7-checkpoint-and-rehydration).

### No recording in a session whose project root is the home directory

- **Limit.** A session whose project root resolves to your home directory records nothing, and every
  Qompack command there refuses or reports that it is inactive. That covers a session started in the
  home directory, one started below a home that is itself a git work tree (a dotfiles repository) in
  a directory with no `.git` of its own, and `QOMPACK_PROJECT_ROOT` naming the home directory. A
  session that started in the home directory stays inactive even if its work moves into a project.
- **Why.** Owner decision D18. The project store would be `~/.qompack`, the directory that already
  holds Qompack's user-wide configuration, calibration file, fallback logs and, on Windows, the
  staged daemon copies; a project store there would mix the two and put project records where
  uninstalling or resetting the user-wide settings would take them. The comparison ignores case on
  Windows and follows symlinks and junctions, so it cannot be spelled around.
- **What Qompack does instead.** It says so once, on `SessionStart`, and writes nothing: no store, no
  log, no lock, no daemon. The MCP tools answer a stable refusal, `status` and `doctor` report the
  reason, and a project below the home directory — with or without its own `.git`, below a plain
  home — works as it always did.
- **Recorded at.** [docs/troubleshooting.md](troubleshooting.md#qompack-is-inactive-in-the-home-directory);
  [docs/architecture.md §2](architecture.md#2-write-set-and-retention); `plans/00-ARCHITECTURE.md` §3.3.

### No performance guarantee on any host

- **Limit.** Qompack makes no performance guarantee. The latency budgets in this repository gate
  CI; they are not promises about your machine.
- **Why.** A wall-clock figure is only judgeable where it is judgeable:
  [ADR 0010](adr/0010-wall-clock-under-coload.md) exists because a measurement taken under co-load
  is not a judgement, and its Addendum 1 records a budget premise that was falsified by a later
  measurement. The tails a shared machine adds are large enough to invert a verdict: the same
  commit, on a single hosted CI runner class, minutes apart, product byte-identical, read a B-A
  p99 of 3.072 ms in the isolated lane and 18.432 ms in the whole-tree job — a failure against the
  15 ms limit that applied then (ADR 0010, Context table; B-A's Windows default is now 50 ms).
  Reference-platform rows remain open in `plans/V5-report.md` §29 item 2, which also records two
  known in-scope regressions awaiting a budget-versus-guarantee decision.
- **What Qompack does instead.** It measures what it can attribute and prints the availability word
  where it cannot — see the `unavailable` per-hook rows in
  [docs/troubleshooting.md §2](troubleshooting.md#2-unknown-capability-or-telemetry).
- **Recorded at.** [ADR 0010](adr/0010-wall-clock-under-coload.md) (Context, "What this does not
  decide", Addendum 1); `plans/V5-report.md` §29.

### No bounded delivery history on disk, and no downgrade across a rotation

- **Limit.** The delivery journals never forget an identity, so the delivery state on disk grows with
  every delivery a project has ever had — about 0.18 GiB per 100,000 deliveries as measured at the
  V6 close-out — and nothing prunes it. And once a store's delivery journal has
  rotated (every 65,536 deliveries), a Qompack build that predates segmented rollover cannot use it:
  it refuses the journal and assigns no observation identity to anything it captures.
- **Why.** A redelivered copy of any past delivery must get its original observation identity back,
  and a session's arrivals must never restart, so every lease, acknowledgement and arrival stays
  resolvable. What is bounded is memory, not storage: between rotations the daemon holds the active
  window, a lookup reads one path of the archive whose depth grows with the logarithm of the history,
  and a store GC pass holds at most the active window and 65,536 carried leases (the next entry); a
  rotation briefly also holds the outgoing window's archive plan and the carried leases, at most
  64 MiB of them. An older build cannot see the later segments, and appending to the original
  journal would re-mint arrival numbers those segments already assigned, so the first rotation makes
  it refuse instead.
- **What Qompack does instead.** It archives rotated windows compactly (one pack file per generation)
  and keeps the refusal fail-closed: pending input is retained for the current build. A backup taken
  before the first rotation is the rollback path. The daemon rotates on its own when the journal
  fills, and warns once per run beforehand, when a project that has never rotated reaches 49,152
  deliveries ([Backup and restore](backup.md) says when to take it).
- **Recorded at.** `plans/CARRIED-DEFECTS.tsv` SP20-D4; `plans/V2-WAVE1-carried-defects.md` §SP20-D4.

### A rotation pauses capture, and the carried leases have two hard bounds

- **Limit.** Every 65,536 deliveries (or 64 MiB of journal) leases and acknowledgements stop while
  the rotation archives the outgoing window: 2.3 to 6.8 s per full window in the V6 close-out's
  measurements on loaded Windows and Linux hosts. And the archived leases that have no
  acknowledgement are carried from segment to segment with two bounds: past 65,536 of them every
  store GC pass halts and collects nothing, so disk use grows; past 64 MiB of them (about 200,000)
  the next rotation refuses, the journal stops leasing, and capture stops. This build has no repair
  for either. A delivery retired by a policy denial is never acknowledged, and neither is a leased
  delivery that is never published, so both count toward the bounds for the life of the project.
- **Why.** A rotation archives the window under a barrier that excludes both journal pipelines, so
  no identity is assigned while the history it must stay consistent with is moving. The carry is how
  store GC knows what old leases still retain without reading every old segment; a pass holds it in
  memory, so it is bounded, and a carry past its file's own bound could not be read back by the next
  rotation, the offline check or `fsck`, so the rotation refuses to write one.
- **What Qompack does instead.** Hooks spool during the pause and the drain leases their deliveries
  afterwards under the same nonce, so the pause loses nothing. A refused rotation stages nothing and
  keeps every later delivery in durable input. Every rotation, halted GC pass and refused rotation is
  a Loud line and a counter, shown by `qompack status` and summarised in `qompack doctor`'s
  `delivery.rollover` row ([Troubleshooting](troubleshooting.md#7-daemon-problems)). Owner decision
  D6 accepted both as documented residuals, and deferred moving the archive off the pause past this
  release.
- **Recorded at.** `plans/V6-CLOSEOUT-CHECKLIST.md` D6; `plans/V2-WAVE1-carried-defects.md` §SP20-D4.

### A delivery made while Windows reports `config.json` missing uses the configuration without it

- **Limit.** On Windows, a rename that replaces `config.json` (how most editors save) can leave the
  name missing for tens of milliseconds while it runs. A hook whose first look at the file falls in
  that moment finds no file and uses the configuration without that layer, so that one delivery is
  recorded, or not, without the file being saved: a `runtime.mode` of `off` or a `runtime.redact`
  addition in it does not apply to that delivery.
- **Why.** At that moment the file does not exist for any reader: on the development host about one
  rename in every 10,000 to 40,000 left the name missing, for 18 to 115 ms, with no reader holding the
  file, whichever rename the writer used. A hook that has not yet seen the file cannot tell that
  moment from a file you deleted. Refusing every delivery whose project has no `config.json` would
  stop recording in every project that has none, and looking again after every miss would delay
  every hook in those projects.
- **What Qompack does instead.** It reads `config.json` with delete sharing, so an editor's save never
  makes a hook's read fail or refuse, and it never takes a file that exists but cannot be read for a
  missing one: `config.Load` warns (`loud`) and the hook path refuses the capture. A hook that saw the
  file and then could not open it looks again for up to 250 ms, reads the file that comes back, and
  goes on without that layer only if the file stays gone throughout.
- **Recorded at.** [docs/architecture.md §2](architecture.md#2-write-set-and-retention);
  `plans/00-ARCHITECTURE.md` §3.2 (owner decision D22).

### A session's end can wait for the next session when the daemon is stopping

- **Limit.** A `SessionEnd` whose `qompack flush` arrives while the project's daemon is stopping —
  it has closed its listener but still holds `.qompack/run/daemon.lock`, as it does for a moment at
  its idle exit — reaches no daemon. The flush starts a new daemon, which cannot take the lock and
  exits, so nothing ends that session now: its end-of-session work (the observer's end of session,
  the terminal-hook marker, the saved sketches, the store GC pass a session end runs) waits until a
  daemon next starts for the project, normally with the next session there.
- **Why.** Only the lock's holder may serve the project (one daemon per project), and a daemon that
  has begun to stop does not serve again. The flush cannot wait for the stop to finish: the host
  gives every plugin's `SessionEnd` hooks one shared 1.5 s budget
  ([architecture §1](architecture.md#1-process-model)). The idle exit comes only after
  `runtime.daemon.idleExitSeconds` with no live session, so there it takes a session that was silent
  that long and then ended just as the daemon stopped.
- **What Qompack does instead.** The flush is written to the project's client spool before the hook
  exits, so it is replayed, not lost: the next daemon's drain replays it and ends the session the
  way it ends one whose flush arrived live. The extra daemon is the "second `qompack daemon` that
  exits at once" of [Troubleshooting](troubleshooting.md#7-daemon-problems).
- **Recorded at.** `plans/V6-CLOSEOUT-CHECKLIST.md` D35(c); `plans/sdd/V6-closeout/w7-spawnclaim/report.md`
  (open issues).

### No cost or price guarantee

- **Limit.** Qompack cannot tell you what a session cost. Reported usage is not an estimated price,
  and an estimated price is not an invoice.
- **Why.** The category counts that a price needs — cache read, cache write, input, output — are not
  exposed to a plugin. `plans/V5-report.md` §25 records cache categories, TTL and retries as
  `unknown` and estimated price as `unknown`, "no rate table applied; subscription allowance is not
  cash per token". `Qompack.md` Appendix A scopes the quantity the same way: "Request price — sum
  reported category × applicable dated rate, plus non-token charges; unknown stays unknown."
- **What Qompack does instead.** Its ledger never writes an estimate as an invoice and never fills
  an unknown count with zero — [docs/user-guide.md](user-guide.md#usage-accounting).
- **Recorded at.** `Qompack.md` v1.5 Appendix A; `plans/V5-report.md` §25;
  [docs/architecture.md §10](architecture.md#10-what-is-not-supported). A host change that would
  lift this is proposal 6 in [docs/upstream-issues.md](upstream-issues.md).

### No name-availability guarantee

- **Limit.** Qompack cannot guarantee that its slash-command or MCP tool names are available in your
  session. This plugin advertises names; nothing reserves them, and another plugin may advertise the
  same ones.
- **Why.** What else a host has loaded is not visible from here, and the matrix that would test it
  has not been run: `plans/V5-report.md` §29 item 6 carries T21-HOST-01, the competing-hook matrix,
  as needing an installed host.
- **What Qompack does instead.** It namespaces its commands (`/qompack:…`) and its tools, and treats
  a collision as an operational question you can see in the host's own command list.
- **Recorded at.** `plans/V5-report.md` §24 (host allowlist empty) and §29 item 6;
  [docs/architecture.md §10](architecture.md#10-what-is-not-supported).

### No inference of cache state from arbitrary elapsed time

- **Limit.** Qompack cannot infer cache state from arbitrary elapsed time. It does not decide that a
  prompt cache entry is warm or cold because a certain number of minutes passed.
- **Why.** The break-even arithmetic is scoped as a diagnostic, not an oracle: `Qompack.md`
  Appendix A prices it as "N > (w−r)/(1−r), assuming N identical uses, one write, later hits and
  r<1". The host exposes no cache-state signal to a plugin, so an elapsed-time guess would be a
  fabricated observation.
- **What Qompack does instead.** It treats an unobserved quantity as `unknown` and leaves
  cache-shaped decisions to the host. Where Qompack itself calls something a cache, it says so and
  bounds the consequence — [ADR 0009](adr/0009-negative-knowledge-bloom-as-cache.md) is about
  Qompack's own Bloom filter, not about the host's prompt cache.
- **Recorded at.** `Qompack.md` v1.5 §12 and Appendix A; `plans/QOMPACK-ERRATA.md` v1.3 "What could
  not be verified, and will not be".

## 5. Trust boundary

### An archive is never a denied-read bypass

- **Limit.** Qompack cannot be used as a denied-read bypass. Retrieving from the archive is not a
  way around a policy decision that refused a read.
- **Why.** The risk register routes this to authorization before snippets and bounded decoding, with
  "no denied-read bypass" as the stated boundary. `denied` means the content exists and was
  withheld; returning it from a second door would invert the decision.
- **What Qompack does instead.** It authorizes before it expands, keeps `denied` distinct from
  `absent`, and never substitutes current content for a denied or unavailable one —
  [docs/troubleshooting.md §4](troubleshooting.md#4-denied-or-unavailable-evidence).
- **Recorded at.** `Qompack.md` v1.5 §12 (both the risk-register row and "What this plugin cannot
  do"); [docs/user-guide.md](user-guide.md#fidelity-coverage-and-error-states).

### It cannot see every host permission rule

- **Limit.** Qompack re-applies the `Read` deny and ask rules saved in Claude Code's settings files
  to every archived retrieval, but it cannot see rules that exist only in the running session: rules
  added with `/permissions` for the session alone, `--allowedTools`, `--disallowedTools`,
  `--settings` and `--setting-sources` flags, PreToolUse hooks that refuse reads, an embedding
  host's managed settings, a managed `policyHelper`'s output, or the session's working directory
  when it is not the project root. Content captured without a path — shell output — has nothing for
  a path rule to match, so `cat .env` archived as shell output is served even under `Read(./.env)`.
- **Why.** The host offers no interface through which an MCP server can ask whether a native Read of
  a path would be allowed now; the settings files are the only part of that decision a plugin can
  read. Checked against the permissions and settings documentation on 2026-09-22.
- **What Qompack does instead.** It reads every settings file the host reads (managed, cached
  server-managed, user, project and local), re-reads one as soon as it changes, refuses an ask rule
  as well as a deny rule, answers `unavailable` ("host policy unavailable") for every path-bearing
  record while a settings file exists but cannot be read or parsed, and takes the more refusing
  reading wherever the documentation leaves one open. The refusal never echoes the path or the rule
  — [docs/security.md §1](security.md#1-trust-boundaries).
- **Recorded at.** `internal/hostperm`'s package comment; the evidence under
  `plans/sdd/V6-closeout/hostperm/runs/`.

### Redaction is applied at capture, and telemetry is hardwired off

- **Limit.** Qompack cannot retroactively redact what it already stored, and it cannot send
  telemetry anywhere. Privacy policy is applied on the capture path; there is no upload path to turn
  on.
- **Why.** A record stored before a policy change was stored under the old policy; that is why
  `redacted` is a capture-time fidelity value. For telemetry, `runtime.telemetry.enabled` is
  hardwired off and a `true` is refused as a violation (`internal/config/validate.go`); decision D10
  forbids network I/O anywhere in this repository, which `internal/mcp`'s package comment restates
  for the one package most likely to be mistaken for an exception.
- **What Qompack does instead.** It writes under `.qompack/` in your project and keeps retention and
  write-set local — [docs/architecture.md §2](architecture.md#2-write-set-and-retention) — and
  `qompack config print --provenance` shows the effective value of every key and where it came from.
- **Recorded at.** `internal/config/validate.go`;
  [docs/config-reference.md](config-reference.md#runtime); `internal/mcp` package comment (decision
  D10); [docs/troubleshooting.md §2](troubleshooting.md#2-unknown-capability-or-telemetry).

## 6. Not in this build

These are the limits that can move. Each names the gate or the owner that would move it.

### No manual checkpoint command

- **Limit.** Qompack does not offer a manual checkpoint: there is no `/qompack:checkpoint` command,
  and no `qompack` subcommand seals one on demand.
- **Why.** The only route such a command could shell out to, `qompack checkpoint`, is the
  `PreCompact` hook entry point: it reads a hook event from stdin and always exits 0. Earlier builds
  shipped a `/qompack:checkpoint` command file that ran it, so the command wrote nothing and reported
  nothing; it is no longer installed. A separate on-demand route has not been built, and it would
  add little: the block injected after a compaction is built from the session's latest checkpoint,
  which is normally the one that compaction sealed.
- **What Qompack does instead.** Checkpoints are written automatically: by the `PreCompact` hook
  just before every compaction, and on the checkpointer's own cadence during a session once enough
  new work has accumulated. `qompack fsck` verifies the checkpoint tier against its manifest.
  `/qompack:status` reports no checkpoint list or latest seq; checkpoint activity shows up there
  only as the `checkpoint_finalize` latency (the `PreCompact` hook row and budget B-E) and as any
  `checkpoint.*` counters the daemon has recorded, such as `checkpoint.cadence.local_seal` or
  `checkpoint.sources.unavailable`.
- **Recorded at.** [docs/commands.md](commands.md) (the preamble: "There is no checkpoint
  command"); [docs/user-guide.md](user-guide.md#checkpoints-are-automatic);
  `internal/pluginmanifest/manifest.go` (`commandSpecs`); `plans/V5-report.md` §29 item 3 (the
  SP-14 handoff edge H3 this closes by not shipping the command).

### No operator backup, restore or rollback command; `bench` unimplemented

- **Limit.** No command takes a backup, restores one, or performs a rollback. The store carries the
  machinery — `TakeBackup`, `VerifyBackup`, `RestoreBackup` and `RehearseRollback`
  (`internal/store/backup.go`) — but it is reachable only from Go, through a migrator the closed
  `store.migrate.legacyImportCutover` build gate refuses to construct. `qompack bench` is also
  unimplemented and prints `qompack bench: not implemented in this build`.
- **Why.** The migration/rollback path stays behind a build gate that has no config key until its
  acceptance evidence lands (`internal/config/migration.go`), and `bench` is carried as an open row
  for a later subplan (`plans/V5-report.md` §29 item 7).
- **What Qompack does instead.** SP-17 shipped the read-only diagnostics `qompack fsck` and `qompack
  doctor`. `fsck` is the recovery **check** — it reports on backup and migration state and, with
  `--repair --yes`, performs five explicit additive repairs that never delete data — and `doctor`
  reports capability, scope and control rows. The interim rollback procedure is
  [docs/release.md §5](release.md#5-rollback).
- **Recorded at.** [docs/user-guide.md](user-guide.md#operator-commands);
  [docs/troubleshooting.md §9](troubleshooting.md#9-backup-rollback-and-recovery);
  [docs/release.md §5](release.md#5-rollback); `plans/V5-report.md` §29 item 7.

### Installation rehearsed on windows/amd64; live sessions unverified

- **Limit.** Installed-host verification exists for **windows/amd64 only**, and only at the install
  and launcher-resolution level. On that one target SP-17 installed the bundle into Claude Code
  2.1.263 and the launcher resolved from the host's plugin cache, so it reads `installed-verified`
  ([docs/release.md](release.md#3-supported-scope) §3). The other five release targets read
  `unknown`, and no target has been exercised in a live session against a live model.
- **Why.** SP-17's record raises only windows/amd64, from a directory install and a launcher that
  resolved in the host's cache; `${CLAUDE_PLUGIN_ROOT}` expansion in a live session and the four host
  contracts below stay unobserved. `qompack self-test` runs them against a zero `daemon.Services`, so
  `hook.additional_context_delivered`, `precompact.has_time_to_write`,
  `precompact.custom_instructions_accepted` and `mcp.server_registered` report `not-yet-implemented`;
  a live daemon declares each of those producers only when the matching seam is bound
  (`internal/daemon/options.go` `DeclareProducers`, guarded by `s.PreCompact`/`s.Checkpoints`,
  `s.Rehydrate` and `s.MCPInitialized`), and what such a run would observe against an installed host
  has never been recorded.
- **What Qompack does instead.** It reports the producer-absent state honestly instead of reading it
  as success, and pins that reading with a test over `internal/contract/observation.go`'s spellings
  table.
- **Recorded at.** `plans/V5-report.md` §24; `plans/MIGRATION-EVIDENCE.md` "Capability register
  inputs"; [docs/troubleshooting.md §1](troubleshooting.md#1-start-with-provenance).

### The enabled-surface rows that are "mechanism only"

- **Limit.** Several SP-21 surfaces exist as code and are not enabled: the admission gate is off (a
  zero gate refuses; explicit opt-in only), replacement is off, the host allowlist is empty, and
  fresh-owned-result transformation, durable capture/authorization before replacement and
  unsupported-content pass-through are recorded as *mechanism only*. The quality row has no
  observations at all.
- **Why.** Each is recorded as *disabled*, never as *passed*: the enabled-surface matrix in
  `plans/V5-report.md` §24 gives the status of every row, and the switches behind them stay refused
  until their gate passes.
- **What Qompack does instead.** It ships them off. A `true` written against a refused switch falls
  back to the default and warns, so a config file cannot enable a capability this build does not
  support.
- **Recorded at.** `plans/V5-report.md` §24 (SP-21 enabled-surface matrix);
  [docs/config-reference.md](config-reference.md#gated-switches-ship-off) and its
  [build gates](config-reference.md#build-gates-no-config-key); README, "What ships off, and why you
  should leave it off".

## How to read the rest of the docs

When a page names a file as **planned (SP-NN)** in plain text rather than linking it, the file does
not exist on this tree: the text is a forward reference to the subplan that owns it, not a broken
link. Anything written as a link resolves to a file that is here now, and a test in `test/docs`
fails the build if it stops resolving.

The same convention governs claims. A behaviour stated on these pages is read from a named source
and cited; where nothing here can settle a question, it is marked `unknown` with the observation
that would answer it, rather than guessed. `unknown` is a real answer and appears as one in the
fidelity, coverage and outcome enumerations
([docs/user-guide.md](user-guide.md#fidelity-coverage-and-error-states)).

Where a limit above could be lifted by a host change, the proposal that would lift it is on
[docs/upstream-issues.md](upstream-issues.md) — prepared text, and nothing more than that.

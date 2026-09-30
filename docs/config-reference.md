# Configuration reference

**This file is generated. Do not edit it by hand.**
Run `go run ./tools/devtool gen-config-docs` after changing `config.Defaults()`;
CI fails if it drifts (§8 `docs` job, §11.4).

Values are resolved from five layers, lowest precedence first:
`config.Defaults()` → `~/.qompack/config.json` → `<project>/.qompack/config.json`
→ `QOMPACK_*` environment → `--set <dotted.key>=<value>` (§11.2). The merge is deep and per leaf, so a project file that sets one key inherits every other default.

An invalid value is never fatal: the offending leaf falls back to its default, the violation is
reported through the `Loud` channel and recorded in `.qompack/state/config-violations.json`,
and loading continues (§11.3). Unknown keys produce a warning, never an error. A value of the
wrong type is ignored with a warning, and the leaf keeps the value from the layer below.

The hooks load configuration by the same per-leaf rules, with two kinds of problem that stop
recording rather than fall back: input the hooks cannot read safely (a config file that does
not parse, is not a plain file or is over its size bound, or an oversized `QOMPACK_*` or
`--set` value), and a setting of `runtime.redact` or `runtime.mode` that cannot be applied as
written, where a fallback would record under a privacy policy you did not write, or record while
you were switching recording off. `qompack self-test` reports either as `config.capture`; see
[docs/troubleshooting.md](troubleshooting.md#6-configuration-and-schema-compatibility).

A default that differs by platform names every value in its Default cell, portable one first.

Run `qompack config print --provenance` to see the effective value of every key and where it came from.

## `checkpoint`

| Key | Type | Default | Valid range | Section | Description |
|---|---|---|---|---|---|
| `checkpoint.budgetTokens` | integer | `12000` | [1000,100000] | §8.5 | target token budget for a single checkpoint artifact |
| `checkpoint.frontier.advanceOnSegmentClose` | boolean | `true` | — | §8.5 | advance the checkpoint frontier incrementally whenever a segment closes |
| `checkpoint.frontier.maxResidualTokens` | integer | `20000` | (0,∞) | §8.5 | maximum tokens between the frontier and the compaction point before a full pass is forced |
| `checkpoint.incrementalSpanInstruction` | boolean | `true` | — | §8.5 | retired (C1.18): no host accepts a PreCompact instruction, so none is emitted; read for compatibility only (docs/cannot-do.md) |
| `checkpoint.tiers.first` | array | `["pointers","narrative"]` | one of `invariants`, `user_intent`, `eliminated`, `decisions`, `open_questions`, `current_work`, `pointers`, `narrative` | §6.9 | checkpoint fields truncated first under budget pressure |
| `checkpoint.tiers.late` | array | `["decisions","open_questions","current_work"]` | one of `invariants`, `user_intent`, `eliminated`, `decisions`, `open_questions`, `current_work`, `pointers`, `narrative` | §6.9 | checkpoint fields truncated only after the first tier is exhausted |
| `checkpoint.tiers.never` | array | `["invariants","user_intent","eliminated"]` | one of `invariants`, `user_intent`, `eliminated`, `decisions`, `open_questions`, `current_work`, `pointers`, `narrative` | §6.9 | checkpoint fields that are never truncated |

## `eliminations`

| Key | Type | Default | Valid range | Section | Description |
|---|---|---|---|---|---|
| `eliminations.defaultScope` | string | `"session"` | one of `session`, `project` | §8.3 | default scope for a new elimination when the caller does not specify one |
| `eliminations.rebuildOnStale` | string | `"nextIdle"` | one of `nextIdle`, `immediate`, `never` | §8.3 | when to rebuild tried.bloom after a dependency hash changes |
| `eliminations.requireEvidence` | boolean | `true` | — | §8.3 | require an evidence hash before an elimination is recorded |
| `eliminations.staleResponse` | string | `"flag"` | one of `flag`, `drop` | §8.3 | how already_tried responds to a stale elimination |

## `eval`

| Key | Type | Default | Valid range | Section | Description |
|---|---|---|---|---|---|
| `eval.minSessions` | integer | `20` | [1,∞) | §11.4 | minimum logged sessions required for a phase-gate replay to be credible |
| `eval.replayOnPhaseGate` | boolean | `true` | — | §11.3 | run the full replay suite at every phase gate |

## `retrieval`

| Key | Type | Default | Valid range | Section | Description |
|---|---|---|---|---|---|
| `retrieval.defaultSpan` | string | `"minimal"` | one of `minimal`, `full` | §8.7 | default span returned by retrieval tools |
| `retrieval.ephemeralResults` | boolean | `true` | — | §8.7 | tag every retrieval result ephemeral at birth so it is the first eviction candidate |
| `retrieval.promoteAfterExpansions` | integer | `2` | [1,∞) | §8.7 | repeated expansions of the same hash before it is promoted into the next checkpoint's pointer tier |

## `runtime`

| Key | Type | Default | Valid range | Section | Description |
|---|---|---|---|---|---|
| `runtime.budgets.checkpointFinalizeMs` | integer | `2000` | (0,∞) | 00-ARCH §2.4 | B-E latency budget: PreCompact entry to exit |
| `runtime.budgets.hookDegradedMs` | integer | `1000` | (0,∞) | 00-ARCH §12.3 | B-G latency budget: the spool append a hook pays when the daemon is unreachable |
| `runtime.budgets.l0IngestMs` | integer | `15` (`50` on Windows, `40` on macOS) | (0,∞) | 00-ARCH §2.4 | B-B latency budget: the daemon's whole ingest.Accept — durable WAL append, delivery lease, seal |
| `runtime.budgets.l0ProcessMs` | integer | `50` | (0,∞) | 00-ARCH §2.4 | B-C latency budget: WAL to fully chunked, stored, DAG/sketches updated |
| `runtime.budgets.mcpToolCallMs` | integer | `250` | (0,∞) | 00-ARCH §2.4 | B-F latency budget: MCP request to response |
| `runtime.daemon.ackDeadlineMs` | integer | `17` (`73` on Windows, `45` on macOS) | — | 00-ARCH §2.4 | deadline for the daemon's one-byte ACK on the hot path |
| `runtime.daemon.connectDeadlineMs` | integer | `5` (`25` on Windows) | — | 00-ARCH §2.4 | deadline for a hot-path client to connect to the daemon |
| `runtime.daemon.enabled` | boolean | `true` | — | 00-ARCH §2.4 | run the resident per-project daemon |
| `runtime.daemon.idleExitSeconds` | integer | `1800` | — | 00-ARCH §2.4 | seconds with zero live sessions before the daemon exits |
| `runtime.daemon.maxSessions` | integer | `8` | — | 00-ARCH §2.4 | maximum concurrent sessions the daemon tracks |
| `runtime.hotPath.breachWindows` | integer | `3` | [1,∞) | §8.1 | consecutive 512-sample windows over budget before the daemon spools instead of syncing |
| `runtime.hotPath.budgetMs` | integer | `15` (`50` on Windows, `40` on macOS) | (0,∞) | §8.1 | B-A hot-path latency budget in milliseconds |
| `runtime.hotPath.maxPayloadBytes` | integer | `1048576` | [4096,∞) | 00-ARCH §2.4 | maximum NDJSON request line size accepted by the daemon |
| `runtime.hotPath.spoolOnBreach` | boolean | `true` | — | §8.1 | degrade to spool-only mode when the hot-path budget is breached |
| `runtime.logging.level` | string | `"info"` | one of `debug`, `info`, `warn`, `error` | 00-ARCH §5.2 | minimum log level written to the day log |
| `runtime.logging.maxFileMB` | integer | `10` | — | 00-ARCH §5.2 | log file size in MB before rotation |
| `runtime.logging.maxFiles` | integer | `5` | — | 00-ARCH §5.2 | number of rotated log files retained |
| `runtime.mcp.maxResponseBytes` | integer | `262144` | [4096,∞) | §8.7 | maximum bytes of result text the expand and re_read tools return (content JSON-escaped, with its envelope); other tools are not measured against it |
| `runtime.mcp.spanWidenLines` | integer | `40` | [0,∞) | §8.7 | lines to widen a minimal span by when the caller requests more context |
| `runtime.migration.capture.rawEvidence` | boolean | `false` | — | Qompack.md v1.5 §8.1 / SP-20 M1-01 | capture permitted raw host payload bytes before any transform (SP-20 M1); refused until the M1 gate passes |
| `runtime.migration.compaction.automaticVeto` | boolean | `false` | — | Qompack.md v1.5 §7.3 / 00-ARCH §12.1 | let the scheduler veto an automatic compaction for optimization; refused: the recovery/proactive distinction is unverified (SP-19 M0-03) |
| `runtime.migration.compaction.blockManualCompact` | boolean | `false` | — | Qompack.md v1.5 §12 / 00-ARCH §12.1 | block a manual /compact for optimization; hardwired false, the key exists only to say so |
| `runtime.migration.experiments.enabled` | boolean | `false` | — | Qompack.md v1.5 Appendix C / SP-15, SP-16 | enable experimental representation and optimizer policies (SP-15/SP-16); refused until their gates pass |
| `runtime.migration.publication.durableFrontier` | boolean | `false` | — | Qompack.md v1.5 §8.2 / SP-20 M1-02 | publish references and the committed frontier only behind an acknowledged durable object write (SP-20 M1); refused until the M1 gate passes |
| `runtime.migration.reinjection.sessionStartCompact` | boolean | `true` | — | Qompack.md v1.5 §8.6 / 00-ARCH §12.1 | reinject the rehydration payload through SessionStart source=compact additionalContext, the one tested injection adapter; false disables injection without touching recording |
| `runtime.migration.replacement.newResult` | boolean | `false` | — | Qompack.md v1.5 §8.7 / SP-21 | replace newly delivered tool results with Qompack handles (SP-21 M4); refused until the M4 gate passes |
| `runtime.migration.settingsVersion` | integer | `1` | [1,1] | Qompack.md v1.5 Appendix C / SP-19 M0 | version of the runtime.migration block; a file written for a newer version has its whole block reset to defaults, so unknown future switches stay off |
| `runtime.mode` | string | `"auto"` | one of `auto`, `full`, `passive`, `off` | 00-ARCH §12 | overall operating mode |
| `runtime.phase7.filters.segmentBloom` | boolean | `false` | — | Qompack.md v1.5 §6 / SP-16 M6 | build a per-segment bloom filter alongside each segment index to skip segments that cannot match (SP-16 §3); refused until the M6-G16-E gate passes |
| `runtime.phase7.retrieval.demandPromotion` | boolean | `false` | — | Qompack.md v1.5 §8.6 / SP-16 M6 | let observed demand promote a representation in the NEXT Qompack injection (SP-16 §2); refused until the M6-G16-C gate passes |
| `runtime.phase7.retrieval.maxAttemptsPerTrigger` | integer | `2` | [1,∞) | Qompack.md v1.5 §8.7 / SP-16 M6 | hard cap on retrieval attempts one trigger may make before it stops and reports |
| `runtime.phase7.retrieval.maxQueueDepth` | integer | `32` | [1,∞) | Qompack.md v1.5 §8.7 / SP-16 M6 | hard cap on queued retrieval work; a full queue drops new work rather than growing unbounded |
| `runtime.phase7.retrieval.maxRemindersPerSession` | integer | `3` | [0,∞) | Qompack.md v1.5 §8.7 / SP-16 M6 | hard cap on retrieval reminders surfaced in one session; 0 emits none |
| `runtime.phase7.retrieval.reminders` | boolean | `false` | — | Qompack.md v1.5 §8.7 / SP-16 M6 | emit bounded retrieval reminders when references change or errors repeat (SP-16 §2); refused until the M6-G16-B gate passes |
| `runtime.phase7.reuse.scopedCandidates` | boolean | `false` | — | Qompack.md v1.5 §8.3 / SP-16 M6 | offer eliminations recorded outside this session as scope-qualified reusable candidates (SP-16 §1); refused until the M6-G16-A gate passes |
| `runtime.phase7.reuse.warmPrior` | boolean | `false` | — | Qompack.md v1.5 §5 / SP-16 M6 | seed a new session with a labeled statistical prior from earlier ones (SP-16 §1); refused until the M6-G16-D gate passes |
| `runtime.phase7.settingsVersion` | integer | `1` | [1,1] | Qompack.md v1.5 Appendix C / SP-16 M6 | version of the runtime.phase7 block; a file written for a newer version has its whole block reset to defaults, so unknown future refinements stay off |
| `runtime.redact.enabled` | boolean | `true` | — | 00-ARCH §5.23 | scrub secrets before content enters the store |
| `runtime.redact.patterns` | array | `[]` | — | 00-ARCH §5.23 | additional user-supplied secret-detection patterns |
| `runtime.rehydrate.eliminationsTopN` | integer | `8` | [1,∞) | §8.6 | number of eliminated approaches surfaced verbatim in the rehydrated digest |
| `runtime.rehydrate.maxTokens` | integer | `12000` | [minTokens,∞) | §8.6 | upper bound of the rehydration token budget; the payload is also held under the host's fixed 9,500-character additionalContext ceiling, which no key raises |
| `runtime.rehydrate.minTokens` | integer | `8000` | [1,maxTokens] | §8.6 | lower bound of the rehydration budget |
| `runtime.rehydrate.skillIndexTokens` | integer | `450` | [1,∞) | §8.6 | token budget for the compact skill index |
| `runtime.scheduler.cache.assumeMaxTTLSeconds` | integer | `3600` | [scheduler.cache.ttlSeconds,∞) | 00-ARCH §11.5 / Qompack.md §5.4 | upper TTL bound the scheduler assumes when the cache regime cannot be identified |
| `runtime.scheduler.cache.expiringTriggerFraction` | number | `0.8` | (0,1) | 00-ARCH §11.5 / Qompack.md §5.4 | fraction of a KNOWN prompt-cache TTL past which the scheduler fires while the prefix is still readable (cache_expiring trigger) |
| `runtime.selection.loopWarningsEnabled` | boolean | `false` | — | 00-ARCH §5.11 | enable state-aware loop warnings; warning-only, bounded and deduplicated when on |
| `runtime.selection.submodularEnabled` | boolean | `false` | — | Closing note | ship-order gate: enable submodular selection; refused without p-selection (closing-note-3) |
| `runtime.telemetry.enabled` | boolean | `false` | — | §7.1 | send telemetry; hardwired off, the key exists only to say so |
| `runtime.tokens.binaryCharsPerToken` | number | `3` | [1,20] | 00-ARCH §10 (G10.2) | baseline characters-per-token estimate for opaque binary content |
| `runtime.tokens.calibrationAlpha` | number | `0.2` | (0,1] | 00-ARCH §10 (G10.2) | exponential-moving-average weight applied to each new calibration sample |
| `runtime.tokens.calibrationMax` | number | `1.6` | [1,∞) | 00-ARCH §10 (G10.2) | upper clamp on the per-project calibration factor |
| `runtime.tokens.calibrationMin` | number | `0.6` | (0,1] | 00-ARCH §10 (G10.2) | lower clamp on the per-project calibration factor |
| `runtime.tokens.codeCharsPerToken` | number | `3.6` | [1,20] | 00-ARCH §10 (G10.2) | baseline characters-per-token estimate for source code |
| `runtime.tokens.diffCharsPerToken` | number | `3.4` | [1,20] | 00-ARCH §10 (G10.2) | baseline characters-per-token estimate for unified diffs |
| `runtime.tokens.imageMaxTokens` | integer | `1600` | — | 00-ARCH §10 (G10.2) | cap on estimated tokens for a single image |
| `runtime.tokens.imagePixelsPerToken` | integer | `750` | [1,∞) | 00-ARCH §10 (G10.2) | pixels per token used to estimate image cost from dimensions |
| `runtime.tokens.jsonCharsPerToken` | number | `3.2` | [1,20] | 00-ARCH §10 (G10.2) | baseline characters-per-token estimate for JSON |
| `runtime.tokens.pdfTokensPerPage` | integer | `1800` | [1,∞) | 00-ARCH §10 (G10.2) | tokens charged per PDF page |
| `runtime.tokens.proseCharsPerToken` | number | `4` | [1,20] | 00-ARCH §10 (G10.2) | baseline characters-per-token estimate for prose |

## `scheduler`

| Key | Type | Default | Valid range | Section | Description |
|---|---|---|---|---|---|
| `scheduler.cache.readMultiplier` | number | `0.1` | (0,1] | §5.1 | prompt-cache read multiplier r |
| `scheduler.cache.ttlSeconds` | integer | `300` | (0,∞) | §5.4 | sliding cache TTL |
| `scheduler.cache.writeMultiplier` | number | `1.25` | [1,∞) | §5.1 | prompt-cache write multiplier w |
| `scheduler.changepoint.features` | array | `["paths","tools","time","todos"]` | — | §6.6 | feature streams BOCD conditions its run-length posterior on |
| `scheduler.changepoint.hazardRate` | number | `0.004` | (0,1) | §6.6 | BOCD prior hazard rate for a changepoint at each step |
| `scheduler.hardCeilingMargin` | integer | `20000` | (0,∞) | §2.5 | token headroom below Claude Code's own auto-compact threshold at which the plugin forces a checkpoint |
| `scheduler.idle.backgroundWork` | boolean | `true` | — | §8.4 | run idle-time background work (shadow checkpoint, GC, slice/Δ-score refresh) |
| `scheduler.idle.deepCutWhenCold` | boolean | `true` | — | §8.4 | prefer a deep cut once the cache is provably cold during an idle gap |
| `scheduler.idle.detectAfterSeconds` | integer | `120` | — | §8.4 | seconds of inactivity before the session is considered idle |
| `scheduler.softFloorPct` | number | `0.55` | (0,1) | §8.4 | fraction of the effective context window below which compaction never fires |
| `scheduler.youngDaly.enabled` | boolean | `true` | — | §6.7 | use the Young-Daly optimal checkpoint interval to pace compaction |
| `scheduler.youngDaly.measuredDeltaSeconds` | number or null | `null` | — | §6.7 | measured compaction cost in seconds; null means measure at runtime |

## `selection`

| Key | Type | Default | Valid range | Section | Description |
|---|---|---|---|---|---|
| `selection.deltaScoring` | string | `"cheap"` | one of `cheap`, `medium`, `expensive` | §8.3 | cost tier of the Δ-scoring implementation |
| `selection.slicing` | string | `"thin"` | one of `thin`, `full` | §8.3 | dependence-DAG slicing variant used to compute the backward slice |
| `selection.submodular.lambda` | number | `0.4` | [0,∞) | §8.3 | redundancy penalty weight in coverage(S) minus lambda*redundancy(S) |
| `selection.submodular.lazyGreedy` | boolean | `true` | — | §6.5 | use lazy greedy evaluation for submodular maximization |

## `sketches`

| Key | Type | Default | Valid range | Section | Description |
|---|---|---|---|---|---|
| `sketches.bloom.capacity` | integer | `10000` | [100,∞) | Appendix A | expected number of entries the tried.bloom filter is sized for |
| `sketches.bloom.fpRate` | number | `0.01` | (0,0.25) | Appendix A | target false-positive rate of the tried.bloom filter |
| `sketches.cms.delta` | number | `0.01` | (0,1) | Appendix A | Count-Min sketch failure probability δ |
| `sketches.cms.epsilon` | number | `0.001` | (0,1) | Appendix A | Count-Min sketch error factor ε |
| `sketches.cms.warmStartFromProject` | boolean | `true` | — | §6.2 | seed the Count-Min sketch from project-scoped history at session start |
| `sketches.hll.registers` | integer | `2048` | power of two in [64,65536] | §6.2 | HyperLogLog register count |

## `store`

| Key | Type | Default | Valid range | Section | Description |
|---|---|---|---|---|---|
| `store.canonicalize.enabled` | boolean | `true` | — | §8.1 | run per-tool canonicalizers before chunking |
| `store.canonicalize.minhash.enabled` | boolean | `true` | — | §8.1 | compute a MinHash signature per stored result to detect near-duplicates |
| `store.canonicalize.minhash.nearDupThreshold` | number | `0.9` | (0,1] | §8.1 | Jaccard similarity above which two results are treated as near-duplicates |
| `store.canonicalize.minhash.permutations` | integer | `128` | [16,512] | §8.1 | number of MinHash permutation functions |
| `store.canonicalize.strip` | array | `["timestamps","ansi","pids","addresses","tmpPaths","durations"]` | one of `timestamps`, `ansi`, `pids`, `addresses`, `tmpPaths`, `durations`, `crlf`, `paths` | §8.1 | canonicalizer classes to strip before chunking |
| `store.chunk.max` | integer | `16384` | (target,∞) | §8.1 | FastCDC maximum chunk size in bytes |
| `store.chunk.min` | integer | `1024` | (0,target) | §8.1 | FastCDC minimum chunk size in bytes |
| `store.chunk.target` | integer | `4096` | (min,max) | §8.1 | FastCDC target chunk size in bytes |
| `store.compression` | string | `"zstd"` | one of `zstd`, `none` | §6.1 | object compression codec |
| `store.retention.days` | integer | `30` | [1,∞) | §8.2 | minimum object retention window in days before GC may collect |
| `store.retention.sessions` | integer | `10` | [1,∞) | §8.2 | minimum object retention window in sessions before GC may collect |

## Provenance origins

`qompack config print --provenance` labels every leaf with the layer that produced its
effective value. These are the labels, lowest precedence first.

| Origin | Meaning |
|---|---|
| `default` | came from `config.Defaults()` and was never overridden |
| `user` | set by `~/.qompack/config.json` |
| `project` | set by `<project>/.qompack/config.json` |
| `env` | set by a `QOMPACK_*` environment variable |
| `flag` | set by a `--set <dotted.key>=<value>` flag |

## Versioned blocks

The blocks below carry their own `settingsVersion` and are versioned independently, so a
schema change to one never resets the other.

| Block | `settingsVersion` this build understands | Behaviour |
|---|---|---|
| `runtime.migration` | `1` | a file written for a newer version has its whole block reset to defaults, so unknown future switches stay off |
| `runtime.phase7` | `1` | a file written for a newer version has its whole block reset to defaults, so unknown future switches stay off |

## Gated switches (ship off)

These leaves default to `false` and stay refused until their gate passes: a `true` value is
refused at load, the leaf falls back to its default and the refusal is reported as a
warning, so editing a config file cannot enable a capability this build does not support.

| Key | Default | Owner | Gate | Status |
|---|---|---|---|---|
| `runtime.migration.capture.rawEvidence` | `false` | SP-20 | M1 capture fidelity (T20-M1-01/02) | pending |
| `runtime.migration.publication.durableFrontier` | `false` | SP-20 | M1 durable publication (T20-M1-03/04/05) | pending |
| `runtime.migration.replacement.newResult` | `false` | SP-21 | M4 admission (T21 pipeline, pass-through and recovery) | pending |
| `runtime.migration.compaction.automaticVeto` | `false` | SP-19 M0-03, then SP-12 | recovery/proactive distinction verified in the target host | pending |
| `runtime.migration.experiments.enabled` | `false` | SP-15 and SP-16 | M5/M6 selection and refinement acceptance | pending |
| `runtime.phase7.reuse.scopedCandidates` | `false` | SP-16 | M6-G16-A scoped reuse and authorization | pending |
| `runtime.phase7.reuse.warmPrior` | `false` | SP-16 | M6-G16-D optional-policy value against a simple baseline | pending |
| `runtime.phase7.retrieval.reminders` | `false` | SP-16 | M6-G16-B bounded retrieval and usefulness telemetry | pending |
| `runtime.phase7.retrieval.demandPromotion` | `false` | SP-16 | M6-G16-C promotion of future representations only | pending |
| `runtime.phase7.filters.segmentBloom` | `false` | SP-16 | M6-G16-E filter coverage, staleness and recovery | pending |

### Build gates (no config key)

These capabilities have no configuration leaf behind them. They cannot be set from any
config layer, and are reachable only from a build whose gate has passed.

| Key | Owner | Gate | Status |
|---|---|---|---|
| `store.migrate.legacyImportCutover` | SP-20 M1-04 | M1 compatible migration (T20-M1-08: import/parity/cutover and the pre- and post-first-write rollback drill) | pending |

## Retired-meaning keys

These keys are still read and their value is still applied, so an existing config file keeps
loading. Setting one from any non-default layer produces a deprecation warning naming the
file and line it was set in, and what the key no longer means.

| Key | What it no longer means |
|---|---|
| `scheduler.youngDaly.enabled` | Young–Daly pacing is compatibility/harness-only: no native compaction trigger, cut or veto depends on it (Qompack.md v1.5 Appendix C; SP-12 reviewed migration) |
| `scheduler.youngDaly.measuredDeltaSeconds` | Young–Daly pacing is compatibility/harness-only: the measured delta no longer times a native compaction (Qompack.md v1.5 Appendix C; SP-12 reviewed migration) |
| `scheduler.idle.deepCutWhenCold` | no native cut is available to a plugin; the key is read for compatibility only and selects no history rewrite (Qompack.md v1.5 §12; SP-12 reviewed migration) |
| `checkpoint.incrementalSpanInstruction` | custom_instructions is PreCompact input, not a summarizer setter, and since C1.18 Qompack emits no PreCompact instruction at all; the key is read for compatibility only (Qompack.md v1.5 §7.3; SP-10 reviewed migration) |

## Reloading the configuration

A running daemon reloads the configuration when the project's `.qompack/config.json` changes
(its size or modification time), which it checks at every session start and on its idle tick.
The hooks and commands load the configuration themselves each time they run. What the reload
does with a changed key depends on the key, and every key it reports as changed is in effect
when it returns. A changed key that needs a restart is named, in its `keys` field, by this line
in `LOUD.log`:

    daemon: config change needs a daemon restart to take effect; the running daemon keeps the value it started with

and a changed key that has no effect in this build by this one:

    daemon: config change has no effect in this build; nothing reads these keys, before or after a restart

The day log's `config reloaded` line lists the keys the reload applied, under `changed`. Those
lines are the reload you can see. The daemon also has an `admin.reload` request, which reloads
whether or not the file changed and answers with the same three lists of keys
(`changed`, `restart_required` and `no_effect`), but it is an IPC op only: no `qompack`
subcommand sends `admin.reload` in this build.
To restart the daemon, let it exit when idle (`runtime.daemon.idleExitSeconds`); the next hook
starts a new one, which loads the whole configuration
([troubleshooting §7](troubleshooting.md#7-daemon-problems)).

A row names one key or every key under it, and the longest matching row decides, so
`runtime.migration.reinjection.sessionStartCompact` takes effect on reload while the rest of
`runtime.migration` needs a restart. A key no row covers is held until a restart.

### Takes effect on reload

Every reader in the running daemon reads these keys at its next use. The reload lists them in its `config reloaded` line and in `admin.reload`'s `changed`.

| Key | Read by |
|---|---|
| `eliminations` | rehydrate service, MCP tools, elimination ledger and scheduler read the live configuration |
| `retrieval.defaultSpan` | MCP tools (ToolDeps.CfgFn) |
| `retrieval.ephemeralResults` | MCP tools (ToolDeps.CfgFn) |
| `runtime.budgets` | budget checks (currentCfg) and the MCP tools (ToolDeps.CfgFn) |
| `runtime.daemon.ackDeadlineMs` | state.bin, rewritten by the reload |
| `runtime.daemon.connectDeadlineMs` | state.bin, rewritten by the reload |
| `runtime.daemon.enabled` | state.bin, rewritten by the reload |
| `runtime.daemon.idleExitSeconds` | the daemon's idle-exit check (currentCfg) |
| `runtime.daemon.maxSessions` | the session registry (applyReloaded) |
| `runtime.hotPath.breachWindows` | the breach detector (applyReloaded) |
| `runtime.hotPath.budgetMs` | the breach detector (applyReloaded) and the hot-path handler |
| `runtime.hotPath.spoolOnBreach` | the hot-path handler (currentCfg) and state.bin |
| `runtime.mcp` | MCP tools (ToolDeps.CfgFn) |
| `runtime.migration.reinjection.sessionStartCompact` | rehydrate service (RehydrateOptions.CfgFn) |
| `runtime.redact` | capture admission (currentCfg), the store's and the retrieval tools' redactors (NewLiveRedactor) |
| `runtime.rehydrate` | rehydrate service (RehydrateOptions.CfgFn) |
| `runtime.selection.submodularEnabled` | rehydrate service (RehydrateOptions.CfgFn) |
| `scheduler.hardCeilingMargin` | scheduler runtime Evaluate (SchedulerRuntimeOptions.CfgFn) |
| `scheduler.idle.backgroundWork` | scheduler runtime idle tasks (SchedulerRuntimeOptions.CfgFn) |
| `scheduler.idle.deepCutWhenCold` | scheduler runtime Evaluate (SchedulerRuntimeOptions.CfgFn) |
| `scheduler.idle.detectAfterSeconds` | idle controller, client-spool watcher and scheduler runtime (applyReloaded) |
| `scheduler.softFloorPct` | scheduler runtime Evaluate (SchedulerRuntimeOptions.CfgFn) |
| `scheduler.youngDaly` | scheduler runtime Evaluate (SchedulerRuntimeOptions.CfgFn) |
| `selection.submodular.lambda` | rehydrate service and scheduler runtime read the live configuration |

### Read by the hook or command when it runs

The hook or command that reads these keys loads the configuration each time it runs, so its next run uses the new value, and any reader in the daemon reads the live configuration. The reload applies them and lists them as changed.

| Key | Read by |
|---|---|
| `eval` | the eval commands, which load the configuration when they run |
| `runtime.mode` | each hook, which loads the configuration when it runs; the daemon's session start reads the live configuration (currentCfg) |

### Needs a daemon restart

Something the daemon built when it started holds these keys. The reload keeps the value the daemon started with, leaves the key out of `changed`, names it in the LOUD line above and in `admin.reload`'s `restart_required`; the next daemon applies it.

| Key | Read by |
|---|---|
| `checkpoint` | the checkpoint writer and the PreCompact/idle seams, wired with the start configuration |
| `retrieval.promoteAfterExpansions` | the MCP expansion promoter, built with its threshold at start |
| `runtime.hotPath.maxPayloadBytes` | the observer's result bound and the MCP server's line bound, set at start |
| `runtime.migration` | gated capabilities wired at start; Validate refuses the gated switches |
| `runtime.phase7` | gated refinements wired at start; Validate refuses the gated switches |
| `runtime.scheduler.cache` | the cache regime, resolved when the scheduler binds a session |
| `runtime.tokens` | the token estimators, built with their constants at start |
| `scheduler.cache` | the cache regime, resolved when the scheduler binds a session |
| `scheduler.changepoint` | the changepoint detector's shape, built with the scheduler runtime |
| `selection.slicing` | the dependence DAG, opened with its slicing variant at start |
| `sketches` | the sketch set and the elimination filter, sized at start |
| `store.canonicalize` | the store's and the observer's canonicalizers, built at start |
| `store.chunk` | the chunker's boundaries, built once by store.Open at start; applying a change mid-session would also fork the dedup space (§11.2) |
| `store.compression` | the store's object codec, fixed when store.Open builds it |
| `store.retention` | the observer's retention stamps, taken from the configuration it was built with |

### No effect in this build

Nothing in this build reads these keys, before or after a restart. The reload keeps the value in effect, leaves the key out of `changed`, names it in the LOUD line above and in `admin.reload`'s `no_effect`.

| Key | Read by |
|---|---|
| `runtime.logging` | no reader in this build: every log is opened at a fixed level and rotation before the configuration loads |
| `runtime.selection.loopWarningsEnabled` | no reader in this build |
| `runtime.telemetry` | hardwired off; Validate refuses true |
| `selection.deltaScoring` | no reader in this build |
| `selection.submodular.lazyGreedy` | no reader in this build: the selector is always lazy (analyzer.NewSelector) |


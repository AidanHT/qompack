# Configuration reference

**This file is generated. Do not edit it by hand.**
Run `go run ./tools/devtool gen-config-docs` after changing `config.Defaults()`;
CI fails if it drifts (§8 `docs` job, §11.4).

Values are resolved from five layers, lowest precedence first:
`config.Defaults()` → `~/.qompack/config.json` → `<project>/.qompack/config.json`
→ `QOMPACK_*` environment → `--set <dotted.key>=<value>` (§11.2). The merge is deep and per leaf, so a project file that sets one key inherits every other default.

An invalid value is never fatal: the offending leaf falls back to its default, the violation is
reported through the `Loud` channel and recorded in `.qompack/state/config-violations.json`,
and loading continues (§11.3). Unknown keys produce a warning, never an error.

Run `qompack config print --provenance` to see the effective value of every key and where it came from.

## `checkpoint`

| Key | Type | Default | Valid range | Section | Description |
|---|---|---|---|---|---|
| `checkpoint.budgetTokens` | integer | `12000` | [1000,100000] | §8.5 | target token budget for a single checkpoint artifact |
| `checkpoint.frontier.advanceOnSegmentClose` | boolean | `true` | — | §8.5 | advance the checkpoint frontier incrementally whenever a segment closes |
| `checkpoint.frontier.maxResidualTokens` | integer | `20000` | (0,∞) | §8.5 | maximum tokens between the frontier and the compaction point before a full pass is forced |
| `checkpoint.incrementalSpanInstruction` | boolean | `true` | — | §8.5 | emit the O1 focus instruction narrowing the summarizer to the span after the checkpoint frontier |
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
| `runtime.budgets.l0IngestMs` | integer | `2` | (0,∞) | 00-ARCH §2.4 | B-B latency budget: daemon read to WAL append returned |
| `runtime.budgets.l0ProcessMs` | integer | `50` | (0,∞) | 00-ARCH §2.4 | B-C latency budget: WAL to fully chunked, stored, DAG/sketches updated |
| `runtime.budgets.mcpToolCallMs` | integer | `250` | (0,∞) | 00-ARCH §2.4 | B-F latency budget: MCP request to response |
| `runtime.daemon.ackDeadlineMs` | integer | `8` | — | 00-ARCH §2.4 | deadline for the daemon's one-byte ACK on the hot path |
| `runtime.daemon.connectDeadlineMs` | integer | `5` | — | 00-ARCH §2.4 | deadline for a hot-path client to connect to the daemon |
| `runtime.daemon.enabled` | boolean | `true` | — | 00-ARCH §2.4 | run the resident per-project daemon |
| `runtime.daemon.idleExitSeconds` | integer | `1800` | — | 00-ARCH §2.4 | seconds with zero live sessions before the daemon exits |
| `runtime.daemon.maxSessions` | integer | `8` | — | 00-ARCH §2.4 | maximum concurrent sessions the daemon tracks |
| `runtime.hotPath.breachWindows` | integer | `3` | [1,∞) | §8.1 | consecutive 512-sample windows over budget before the daemon spools instead of syncing |
| `runtime.hotPath.budgetMs` | integer | `15` | (0,∞) | §8.1 | B-A hot-path latency budget in milliseconds |
| `runtime.hotPath.maxPayloadBytes` | integer | `1048576` | [4096,∞) | 00-ARCH §2.4 | maximum NDJSON request line size accepted by the daemon |
| `runtime.hotPath.spoolOnBreach` | boolean | `true` | — | §8.1 | degrade to spool-only mode when the hot-path budget is breached |
| `runtime.logging.level` | string | `"info"` | one of `debug`, `info`, `warn`, `error` | 00-ARCH §5.2 | minimum log level written to the day log |
| `runtime.logging.maxFileMB` | integer | `10` | — | 00-ARCH §5.2 | log file size in MB before rotation |
| `runtime.logging.maxFiles` | integer | `5` | — | 00-ARCH §5.2 | number of rotated log files retained |
| `runtime.mcp.maxResponseBytes` | integer | `262144` | [4096,∞) | §8.7 | maximum bytes an MCP tool response may return |
| `runtime.mcp.spanWidenLines` | integer | `40` | [0,∞) | §8.7 | lines to widen a minimal span by when the caller requests more context |
| `runtime.mode` | string | `"auto"` | one of `auto`, `full`, `passive`, `off` | 00-ARCH §12 | overall operating mode |
| `runtime.redact.enabled` | boolean | `true` | — | 00-ARCH §5.23 | scrub secrets before content enters the store |
| `runtime.redact.patterns` | array | `[]` | — | 00-ARCH §5.23 | additional user-supplied secret-detection patterns |
| `runtime.rehydrate.eliminationsTopN` | integer | `8` | [1,∞) | §8.6 | number of eliminated approaches surfaced verbatim in the rehydrated digest |
| `runtime.rehydrate.maxTokens` | integer | `12000` | [minTokens,∞) | §8.6 | upper bound of the rehydration budget |
| `runtime.rehydrate.minTokens` | integer | `8000` | [1,maxTokens] | §8.6 | lower bound of the rehydration budget |
| `runtime.rehydrate.skillIndexTokens` | integer | `450` | [1,∞) | §8.6 | token budget for the compact skill index |
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


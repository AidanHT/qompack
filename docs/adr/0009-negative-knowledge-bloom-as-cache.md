# 9. Negative knowledge: the bloom is a cache, and nine decisions that follow from it

Date: 2026-08-23

## Status

Accepted. Implemented by SP-09 (`internal/negknow`).

## Context

Qompack.md §8.3 frames every elimination as a *conditional* fact:

> An elimination is a *conditional* fact: "widening the pool timeout doesn't work given
> pgbouncer 1.18." Upgrade pgbouncer and the elimination is void — and a false `already_tried`
> that blocks a now-viable approach inverts the feature from asset to liability. Standard Bloom
> filters cannot delete, so the design is: 1. The structured `eliminated[]` records are the
> source of truth; the Bloom filter is only a cache over them. This was implicitly true; it is
> now load-bearing.

00-ARCHITECTURE.md §13 invariant 3 turns that into a mechanical rule:

> The bloom filter is a cache, never the source of truth (§8.3). Every membership answer is
> backed by a record lookup or explicitly flagged `BloomOnly`.

`internal/negknow` is now complete on this branch: `descriptor.go`, `classify.go`, `record.go`,
`log.go`, `ledger.go`, `staleness.go`, `bloom.go` and `dagedges.go`. This ADR records the nine
decisions the plan named and one controller ruling (R6) at the seam with SP-07's `internal/dag`,
each checkable against the shipped code.

## Decision

### 1. Two bloom keys per record: `Key()` identity, `MatchKey()` reason-independent

`Descriptor.Key` (`internal/negknow/types.go:108-119`) digests all four fields — path, symbol,
approach class and the raw `ReasonHash` bytes — under `core.DomainNegKnow`. `Descriptor.MatchKey`
(`internal/negknow/descriptor.go:159-170`) digests only path, symbol and approach class under the
separate domain `qompack.neg.match.v1`, deliberately excluding `ReasonHash`. `Query` canonicalizes
with an empty reason and tests `MatchKey` (`ledger.go:783-784`), because `already_tried` is asked
with a target and an approach but no candidate reason — a key folding in `ReasonHash` could never
be looked up by a caller who does not know what reason to predict. `Record` therefore inserts both
keys on every append (`ledger.go:753-754`), which is why the rebuild's capacity math is
`2 * len(vis)` (`bloom.go:62`) and why the descriptor doc states "effective bloom capacity
consumption is 2 x records" (`descriptor.go:156`).

### 2. A record line is a bare `Record`; control lines carry an `op` key

`logProbe` (`log.go:78-86`) is what every JSONL line is decoded into first; its only field is
`Op logKind`. No `Record` field ever emits `"op"` — `recordWire` (`record.go:141-156`) has no such
key — so a record line probes to `""` and the one control line this version writes, `op:"stale"`
(`log.go:66-68`), probes to `"stale"`. `replayLog`'s switch (`log.go:172-216`) branches on exactly
that presence or absence. The frozen fixture settles it: `testdata/golden/contracts/negknow/want/
elimination_record.jsonl` is byte-frozen (Rule W-2) and carries no `"op"` key at all. Since that
shape can never gain a field, presence-of-`op` is the only discriminator that both matches the
frozen record shape and lets an unrecognized future op arrive from a newer plugin version without
the reader choking — `log.go:83-84`: "a record may never grow an op field, and ... an unrecognized
op is skipped rather than rejected."

### 3. `stale` as an append-op rather than a rewrite (§7.4)

`markStaleLocked` (`staleness.go:76-79`) appends `logControl{Op: opStale, ID: id, TS: ...,
Because: because}` through `appendLine`; it never rewrites the record's own line. The log is opened
only through `paths.AppendOnly` (`openLog`, `log.go:129-142`), which "never asks for O_TRUNC"
(`log.go:25-26`) — an in-place rewrite is not a code path this package can reach, mirroring
00-ARCHITECTURE.md §7.4's mechanical enforcement of append-only `*.jsonl` files. `replayLog`
applies an `op:"stale"` line by REPLACING the record's in-memory staleness fields
(`log.go:195-211`: `Status`, `StaleSince` and `StaleBecause` are all overwritten), which is what
makes a re-flip idempotent even though the log itself only ever grows. Qompack.md §8.3 item 3:
"When a dependency hash changes, the elimination flips to `status: "stale"`. On the next idle
window, `tried.bloom` is rebuilt from active records only." An append-only log has no way to edit
an already-written field, so the flip has to be a new fact recorded after the old one, never an
edit of it — that is why the mechanism is shaped as a control line and not a rewrite.

### 4. Coarse approach classes: a deliberate recall/precision trade

`ApproachClass` (`classify.go:82-109`) lowercases, splits on non-alphanumeric runs, drops 48
stopwords, and maps surviving tokens through a closed synonym table (`classify.go:39-63`) — e.g.
`bump`/`widen`/`increase`/`raise`/`enlarge` all stem to `widen` (`classify.go:41-42`).
`Canonicalize`'s doc (`types.go:83-89`) states the intent: "The classification is deliberately
coarse ... so two phrasings of one idea collide on purpose. That only widens the set of records a
query can match; the record lookup stays authoritative and `Record.Reason` is what disambiguates."
Coarsening raises recall — a query phrased differently than the original record still shares a
`MatchKey` — at the cost of precision, and `Query` absorbs that cost safely because a `MatchKey`
hit is only ever a candidate set filtered by `Status`, never an answer by itself
(`ledger.go:797-808`).

The frozen fixture's `approach_class:"widen-timeout"` is a format label, not a `Canonicalize`
assertion: `types.go:87-89` says so directly — "Never compare this output against
`testdata/golden/contracts/negknow/want/`, whose approach_class ("widen-timeout") is a hand-chosen
label in a frozen wire-shape fixture ... which yields "widen-pool-timeout" for that same phrase."
Running the shipped algorithm on `"widen pool timeout"` produces three surviving stems — `widen`,
`pool`, `timeout`, none dropped by the four-token cap (`classify.go:8`) — joined as
`widen-pool-timeout`, not the fixture's two-token spelling. The codec never recomputes or validates
`approach_class` (`record.go`'s `descWire`), so the mismatch has no effect on decoding.

### 5. Why a `nextIdle` rebuild cannot cause a wrong answer

`Record` adds both bloom keys to the in-memory filter synchronously on every successful append
(`ledger.go:753-754`), regardless of `eliminations.rebuildOnStale`; that setting only governs when
the change is persisted to `sketches/tried.bloom` (`staleness.go:93-100`, `bloom.go`'s
`RebuildBloom`/`persistBloom`), tracked by the `pending` flag (`ledger.go:304`,
`staleness.go:98-100`). A query issued immediately after `Record` already sees the new key.

Across a restart, `Query`'s structure prevents a stale on-disk filter from producing a wrong
answer: every path returning `AnswerActive` or `AnswerStale` requires a materialized record behind
the `MatchKey` hit (`ledger.go:797-827`); a hit with no backing record answers
`AnswerAbsent, BloomOnly: true` (`ledger.go:804-808`) — the safe direction under §13 invariant 3.
The only way a stale on-disk filter could matter is a MISSING key, and `reconcileBloom` always
rebuilds synchronously at the next `Open` when `bloom.Count() < want` (`ledger.go:551-561`:
"Missing keys are false NEGATIVES, which defeat the feature without any symptom. This is the
common case"). A deferred `nextIdle` rebuild is therefore recovered at the very next `Open`
whether or not the idle window ever ran, and the one direction §13 forbids — a false negative —
cannot survive either a restart or an in-process query.

**Addendum — R25:** the guarantee above is that a rebuild never produces a WRONG answer; it is
silent on a stale answer's LIFETIME, and those are different guarantees. `rebuildLocked` builds
the filter from `visibleActive()` alone (`bloom.go:60-77`), and `visibleActive` filters to
`StatusActive` (`ledger.go:610-618`) — so a stale record's keys are dropped from the filter a
rebuild produces, not carried forward into it. `Query` then short-circuits on that miss
(`ledger.go:792`): a `MatchKey` no longer present in the bloom answers `AnswerAbsent` before the
record is ever consulted. The consequence is concrete: the identical query that answered
`AnswerStale` immediately before a `RebuildBloom` answers plain `AnswerAbsent` immediately after
one — a stale answer does NOT survive a rebuild, even though `Get`, `All` and `Health` still
surface the underlying stale record untouched. This is deliberate rather than a gap: §8.3 scopes
"false positives are safe" to current evidence, and forgetting a record whose own status already
told the caller not to trust it is not the false-negative direction §13 forbids — that invariant
protects MISSING records, not ones a caller was already told are stale. Ruling R25 for SP-11 and
SP-13: neither may assume a stale `already_tried` answer persists past a rebuild — `Query` alone
reverts to `AnswerAbsent`, and only `Get`/`All`/`Health` still know the record was ever stale.

### 6. Why the rebuild honours the configured capacity instead of over-allocating

`rebuildLocked` starts from the configured `capacity, fpRate` (`bloom.go:61`) and grows only when
the record count demands it: `if need := 2 * len(vis); need > capacity { capacity =
roundUpPow2(need) }` (`bloom.go:62-67`). Appendix A's sizing formula gives the shipped default:
"n = 10_000, p = 0.01 → m ≈ 95_850 bits ≈ 12 KB, k = 7" (Qompack.md, Appendix A, bloom filter
sizing). `TestRebuildBloom_HonoursConfiguredCapacity` (`bloom_test.go:152-166`) seeds 3,000 active
records (6,000 keys) and asserts the rebuild lands at exactly the configured 10,000-entry capacity,
"not silently multiplied" (`bloom_test.go:162`); `TestBloomFileSize` (`bloom_test.go:185-198`)
asserts the resulting file lands in 11,264-14,336 bytes — the ~12 KB Appendix A predicts, plus the
sketch header and CRC. Growth is reserved for when the configured size genuinely cannot hold the
keys: `TestRebuildBloom_Resizes` (`bloom_test.go:169-183`) seeds 8,000 active records (16,000 keys)
and the rebuilt filter grows to at least 16,384. 00-ARCHITECTURE.md's daemon warm-start budget
names `sketches/tried.bloom (~12 KB)` as one line item of a ~68 KB read (00-ARCHITECTURE.md:75);
over-allocating unconditionally would make every project pay headroom most sessions never use, so
honouring the configured value is what keeps a normal project's filter at the size Appendix A
actually budgets.

### 7. Why persistence goes through `sketch.ReplaceGenerational`, seeded from the backup files

`persistBloom` writes through exactly one door: `sketch.ReplaceGenerational(filepath.Join(
l.lay.Sketches, sketch.TriedBloomBase), nb, l.seq)` (`bloom.go:114-116`). Its comment gives the
reason (`bloom.go:106-109`): `paths.WriteAtomic` refuses `tried.bloom` via `paths.IsProtected`, and
`sketch.Save` refuses it too — `ReplaceGenerational` is §3.3's sole exception, and it alone owns the
rename to a parseable backup name, the staging/swap, the one-generation prune, and the checked
rollback on a staging failure; re-implementing any of that here would be a second, divergent copy
of logic §3.3 already assigns to `sketch` (a grep-based test, named at `bloom.go:22-26`, proves
there is exactly one `sketch.RebuildBloom` call site and no `os.Rename` in this package).

`l.seq` is seeded at `Open` from `paths.HighestBloomBackupSeq(l.lay)` (`ledger.go:370-375`), not
from a private state file, because — `ledger.go:305-309` — "nothing else on disk records it, and a
private counter file that could disagree with the backup names would be a second source of truth
for a number the filesystem already carries." The `tried.bloom.<seq>.bak` files already encode the
highest surviving generation; a private "next seq" file could desync from them if its own write
failed or a backup was deleted by hand, and `ReplaceGenerational`'s pre-flight would then either
refuse a colliding seq or silently resume below a pruned one. `persistBloom`'s error path
(`bloom.go:124-131`) re-seeds `l.seq` from disk specifically on `sketch.ErrMalformed` — exactly the
"the counter did not resume above the surviving backup" case — so the filesystem stays authoritative
on every path that can drift. `TestRebuildBloom_SeqResumesFromDisk` and
`TestRebuildBloom_PersistenceFailureReseedsSeq` (`bloom_test.go:241`, `bloom_test.go:262`) pin both
halves.

### 8. Why identity dedup is keyed on `(session, scope, Desc.Key())`, not on the record ID

`dedupHex` (`ledger.go:620-635`) hashes `r.Session`, `r.Scope` and `r.Desc.Key()` under
`domainDedup`. It is explicitly not the record ID, because `recordID` mixes in the timestamp
(`record.go:117-132`): "It mixes in ts deliberately, which is why the ledger's dedup is
identity-based (byKey) and not id-based: an MCP retry a second later mints a different id for the
same elimination" (`record.go:121-122`). Were `Record`'s dedup check (`ledger.go:701-710`) keyed
on ID instead, two calls describing the identical elimination a second apart would mint two
different IDs and never collide, defeating the entire point of dedup — making a retried MCP call
or a repeated heuristic-detector proposal a no-op rather than a duplicate log line
(`ledger.go:701-703`: "recording it twice is a no-op, not an error, because MCP retries and the
heuristic detector both re-propose"). Session and scope are folded in alongside `Desc.Key()`
because visibility is part of identity here: the same elimination recorded once session-scoped and
once project-scoped are two distinct facts about who can see it, not duplicates of one fact.

### 9. Why `blind` mode never rebuilds

`rebuildLocked`'s first substantive check is blind mode (`bloom.go:43-48`): "The records that would
feed the rebuild are unreadable, so rebuilding would replace a good on-disk cache with an empty one
on the strength of a transient I/O error. This is the only use of `ErrBlind`" — the sentinel itself
is declared at `ledger.go:206-209`. `reconcileBloom`, which decides whether `Open` should rebuild at
all, returns immediately under blind (`ledger.go:545-548`) for the identical reason. `NeedsRebuild`
(`staleness.go:247-256`) reports false under blind "because ... the rebuild would be refused if it
were attempted, and an idle controller should not be asked to schedule work that cannot run", and
`MaintenanceTask` (`staleness.go:265-282`) skips both `RefreshStaleness` and `RebuildBloom` under
blind or after `Close`. The invariant this protects is §13's: when the log could not be read at
`Open` (`goBlind`, `ledger.go:457-466`, reached from `loadRecords`, `ledger.go:424-455`), the ledger
holds zero records known to be complete, so any rebuild from "what's in memory" would produce a
filter smaller than the true set — the one false-negative direction §13 invariant 3 forbids.

## Consequences

### 10. Ruling R6 — DAG emission follows SP-07's builder, not the plan's hand-built edge

`emitDAG` (`dagedges.go:40-53`) calls
`dag.BuildElimination(l.deps.Graph, dag.EliminationSpec{RecordID: r.ID, TS: r.TS,
PathKey: r.Desc.NormalizedPath, Symbol: r.Desc.Symbol})`. `BuildElimination`
(`internal/dag/builders.go:453-488`) is SP-07's own builder: it points edges file→elimination via
`EdgeSharedFile` (`builders.go:475-480`) and symbol→elimination via `EdgeSharedSymbol`
(`builders.go:481-486`) — the reverse of what the plan originally specified, a hand-built
elimination→file `EdgeExplains` edge: `plans/V3-SP-09-negative-knowledge.md:1319` specifies
"one edge `{From: NodeIDFor(r), To: FileNodeID(r.Desc.NormalizedPath), Kind: dag.EdgeExplains,
Weight: 1}`"; line 398's bare `// EdgeKind members used here: dag.EdgeExplains, dag.EdgeConsumes,
dag.EdgeSharedFile` lists the same kind with no direction. `emitDAG` passes no `Evidence` node
either: this package's evidence is a content-store root, not a graph node, so there is nothing
to name (`dagedges.go:38-39`).

The controller resolved the conflict as ruling R6: `emitDAG` uses SP-07's `dag.BuildElimination`
instead of hand-building the plan's elimination→file `EdgeExplains` edge, because edge semantics
belong to SP-07 (the plan's own out-of-scope table) and the builder's doc comment names this exact
use. That doc comment (`builders.go:440-452`) makes the direction's purpose explicit: "Every edge
points INTO the elimination, because the elimination is the CONCLUSION: the searches that came
back empty explain it, and the file and symbol they searched are the state it is about. A backward
slice from an elimination therefore returns the work that produced it — which is what §8.3's
staleness question ("is this still true?") needs to be answerable at all." 00-ARCHITECTURE.md §13
invariant 3 is why a wrong direction here would be more than cosmetic: an elimination's staleness
question is answered by walking BACKWARD from the elimination node to the work that produced it,
and file/symbol→elimination edges are what makes that walk exist at all.

Consequence for SP-11: because the edge direction is file/symbol → elimination, SP-11 must rank
eliminations by scoring a **backward** slice into the elimination node — the direction
`dag.BackwardSlice` already walks (see `docs/adr/0007-dag-slices-are-scores-not-drop-decisions.md`)
— not a forward slice out of a file node. Getting this backwards costs SP-11 one line to fix, not a
redesign: the node IDs (`NodeIDFor`, `FileNodeID`, `dagedges.go:21,26`) and their frozen spellings
in `testdata/golden/contracts/negknow/node-ids.json` stay exactly as specified either way; only the
slice direction SP-11 calls changes.

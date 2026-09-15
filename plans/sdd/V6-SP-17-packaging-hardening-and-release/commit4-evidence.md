# Commit 4 evidence — `test(fault): exercise capture and lifecycle failures`

Host: Windows 11, amd64, Go 1.26.6. Bundle: assembled by `go run ./tools/devtool bundle --target
windows/amd64` from this branch, once per test binary. Records:
`commit4-fault-windows-amd64/` (41 records plus `INDEX.json`, written by the collecting run).
Every record names the bundle it drove: version `v0.2.0-606-g21e6914-dirty`, binary sha256
`1b55a8731bbf9290c3fe3699f3d2f344ac4d3b059fa235bce1152b622166071d`. The `-dirty` suffix is honest
rather than a defect — the collecting run is taken before the commit exists, so the working tree
it describes is this commit's own uncommitted content.

Outcome vocabulary (test/fault's package doc is normative):

- **recovered** — the cut introduced no dangling reference and the product went on recording.
- **explicit_incomplete** — something still does not resolve, or recording did not resume, AND the
  product says so: a gap in `status --json`, a not-ok row in `self-test --json`, a LOUD.log line, a
  checkpoint DropEntry, or a reference the audit itself can name as reported.
- **failed** — a measured divergence with an owner. Every one is RETURNED (§6), never fixed here.
- **skipped** — the case did not run. A skip is never a pass. This run produced none.

Totals: **26 recovered, 6 explicit_incomplete, 9 failed, 0 skipped** over 41 rows.

Every cell of the §1-§5 tables below is generated from the 41 records by a script that reads each
record's `outcome`, `dangling_before` and `dangling_after` and diffs them against this document, run
after the collecting run and again before this file was last edited. It reports zero drifting cells.
It is worth saying why that machinery exists: the previous revision published `stale drain.json |
0 → 2` beside a record saying `dangling_after: 1`, and an `spool_files=4` beside a record saying 3.

That row is also the one number here that is NOT stable between runs. `stale_drain_progress` leaves
one or two unresolved references and three to five spool files depending on where the killed
daemon's burst happened to stop, so its cells are a READING of the committed record rather than a
constant of the product. Anyone re-running the matrix must regenerate these tables rather than assume
them.

`dangling before → after` are the consistency audit's unreported-reference counts either side of the
recovery. The verdict is taken on the DELTA — SP17-M7-04 says "no NEWLY dangling pointer" — and also
requires `dangling_after == 0`, or every remaining reference named as reported, before a row may say
`recovered`. A row can therefore carry a non-zero "after" and still pass where the references are the
cut's own subject matter, which its reason then says.

Two disciplines the round-1 evidence did not have, and both changed outcomes:

- **Degradation is read with the recovery daemon UP**, then deltaed against a baseline taken the same
  way. `StatusReport.Snapshot` is the daemon's; with the daemon stopped, `internal/commands` falls
  back to the disk source and the whole snapshot branch — the spool count, the LOUD tail, every
  degradation counter including `store.index.badline` — is absent. Round 1 asked after shutting the
  daemon down, so four rows recorded "nothing reported" about gaps the product does report.
- **Evidence must NAME the cut.** Every row declares the tokens that would constitute the product
  reporting its own failure, and the record carries both what the surfaces gained and the subset that
  named the cut. Without that, an unrelated warn a recovery session emits on every run promoted three
  rows to `explicit_incomplete`. Declaring the tokens is not optional any more: a row that declares
  none fails, because five rows had none and were accepting whatever the run happened to log. Nor is
  any token allowed that the harness rather than the product can produce — `dayLogWarnings` prefixes
  each line with the log's file name, "qompack" ends in "ack", and one row's `ack` token therefore
  matched every day-log line in the run. `TestFault_LinesNamingIgnoresTheUniversalWarn` audits every
  row's list against the lines a run emits whatever was cut.

## 1. Publication boundaries (SP17-M7-04)

| boundary | seed method | outcome | dangling | returned as |
|---|---|---|---|---|
| object written but index line absent | last `index/roots.jsonl` line removed, objects left on disk | failed | 0 → 2 | F4-1 |
| roots.jsonl last line truncated mid-record | file cut 24 bytes inside its final record | explicit_incomplete | 0 → 5 | F4-2 |
| tool_use.jsonl truncated | file cut 24 bytes inside its final record | explicit_incomplete | 0 → 1 | F4-3 |
| capture sidecar at stage 1 only | a real sidecar rewritten to the product's own stage-1 shape: `published:false`, zero root, `bytes_hash` retained | failed | 0 → 1 | F4-4 |
| checkpoint artifact without its MANIFEST line | a second artifact written through `paths.CreateNew` with no manifest append | failed | 0 → 1 | F4-9 |
| MANIFEST line whose artifact is missing | the sealed artifact removed, its manifest line kept | failed | 0 → 1 | F4-9 |
| MANIFEST digest mismatch | one bit flipped inside the sealed artifact | failed | 0 → 1 | F4-9 |
| WAL segment truncated mid-record | daemon killed mid-burst, then the surviving `wal-` segment cut | explicit_incomplete | 0 → 0 | — |
| client spool truncated | daemon killed mid-burst, then the surviving `client-` file cut | explicit_incomplete | 0 → 0 | — |
| stale drain.json | every drain record's size and offset doubled | explicit_incomplete | 0 → 2 | F4-7 |
| delivery seal torn slot + half-present pair | lease position torn, ack position removed | recovered | 0 → 0 | — |
| retention-roots file truncated | `state/retention-roots.jsonl` cut inside its final record, then a forced GC pass measured | failed | 0 → 1 | F4-5 |
| state.bin corrupt | the `state-corrupt` injection site, from a real hook process | recovered | 0 → 0 | — |
| config.json corrupt | the `config-corrupt` injection site, from a real hook process | explicit_incomplete | 0 → 0 | F4-6 |

Two rows needed a seed the ordinary session does not leave:

- The three checkpoint rows drive a real PreCompact first. On this host one seeded session does not
  reach §8.5's cadence thresholds, so the hook seals nothing and the artifact is written through the
  product's own writers (`checkpoint.Marshal`, `paths.CreateNew`, `paths.AppendManifest`, which
  `IsProtected` makes the only legal manifest writer). Each record says which of the two happened.
- The three spool rows kill a real daemon mid-burst, because a graceful shutdown drains and removes
  every segment. Their degradation baseline is taken INSIDE the burst, before the kill: asking
  `status --json` afterwards spawns a daemon that drains away the very file the row cuts, which is
  how the WAL row skipped itself once. A killed daemon's lock stays honoured until `daemon.hb` goes
  stale (90 s), so the seed moves that file's mtime into the past rather than waiting it out; no
  timing claim is made either way and the reclaim exercised is the product's own code path.

No outcome in this table moved this round, but two rows' evidence did, in opposite directions, and
both were token defects rather than product behaviour:

- **delivery seal torn slot** was crediting the universal `store: segment closed without a tokens
  feature` warn as the product reporting its cut, because its `ack` token matched the
  `qompack-<day>.log: ` prefix on every day-log line. With that token replaced by the two seal files
  and the two counters only a seal failure spells, the row's evidence is one line and it is the right
  one: `counter l0_delivery_unleased=1` on the live status snapshot.
- **state.bin corrupt** briefly credited the LOUD line the corruption plausibly causes, by declaring
  `contract`, `degrad` and `passive` as tokens. That was withdrawn, and the withdrawal is the more
  useful finding: `lifecycle_frontier_state_is_explicit` cuts NOTHING and carries the identical
  `contract: degrading to passive recording ... sentinel not found after two chances` line five
  times, so it is something a fault-suite session emits, not the product reporting this cut. The
  tokens are back to `state` and `state.bin`, the named subset is honestly empty, and the record now
  carries the reading as a NOTE marked an inference: `run/state.bin` holds the host-contract
  observations, so the chain state.bin corrupt -> the observation is lost -> the self-check fails ->
  passive is plausible and was NOT established here. The row stays `recovered` either way, because
  recording resumed and nothing dangles.

## 2. Lifecycle

All thirteen sequences drive one project against one live daemon, so a row sees the state its
predecessors left. Every row asserts the same three things: each hook exited 0 with parseable stdout
(§13 invariant 6), no `tool_use_id` appears twice in the index, and the sequence left no dangling
reference.

| case | sequence | outcome | dangling |
|---|---|---|---|
| startup | SessionStart(startup) + turn + SessionEnd | recovered | 0 → 0 |
| resume | startup + turn, SessionStart(resume) on the same id + turn | recovered | 0 → 0 |
| fork | SessionStart(source: fork) + turn | recovered | 0 → 0 |
| clear | startup + turn, SessionStart(clear) + turn | recovered | 0 → 0 |
| compaction manual | turn, PreCompact(manual), SessionStart(compact), turn | recovered | 0 → 0 |
| compaction auto | turn, PreCompact(auto), SessionStart(compact), turn | recovered | 0 → 0 |
| compaction failed | turn, PreCompact(auto), NO compact SessionStart, turn | recovered | 0 → 0 |
| compaction crossed | PreCompact on one session, SessionStart(compact) on another | recovered | 0 → 0 |
| duplicate tool_use | the identical PostToolUse payload delivered twice | recovered | 0 → 0 |
| duplicate PreCompact | the identical PreCompact payload delivered twice | recovered | 0 → 0 |
| missing turn | turns 1 and 3 delivered, turn 2 never | recovered | 0 → 0 |
| out-of-order PreCompact | PreCompact before any tool event of the session | recovered | 0 → 0 |
| out-of-order SessionEnd | tool event, SessionEnd, then the Stop that should have preceded it | recovered | 0 → 0 |
| frontier state is explicit | `status --json` + `self-test --json` with the matrix's daemon still up, `checkpoint.Reader.List` against the artifacts on disk, and a full store audit | recovered | 0 → 0 |

`fork` is answered rather than assumed: the hook exits 0, the payload's `source` is carried, and the
turn is indexed. `fork` is not one of the four sources `internal/observer/session.go` names, so the
value is transported but not branched on. That is recorded as the observation it is, not as support
for a fork-specific behaviour that does not exist.

No duplicate `tool_use_id` and no duplicate root CONTENT record appeared in any row; every row also
asserts that the turn it delivered was actually indexed, so a daemon that died mid-matrix would fail
the row rather than yield a vacuous `recovered`.

The missing-turn row states what it can observe and no more: an undelivered turn leaves no trace for
the product to detect, so the row measures that the turns either side of the hole are intact rather
than that the hole was noticed.

## 3. Child failure

| case | outcome | dangling | what it established |
|---|---|---|---|
| MCP server killed mid-request | recovered | 0 → 0 | the client sees the session end as an error rather than hanging, and every object present before the MCP session is still present with the same bytes — hashed and compared, not inferred from the audit counts |
| daemon killed mid-ingest | recovered | 0 → 0 | the restarted daemon replays the spool the killed one left and goes on recording |
| `observe stop --subagent` with no result | recovered | 0 → 0 | the hook exits 0 and leaves no dangling reference. It is NOT inert, and the record now derives what it says from the counts rather than asserting them: tool_use records 0→1, roots 0→1, capture sidecars 0→1. The earlier text claimed the stop "produced no tool_use record" beside a record showing one |
| hook stdin EOF (six subcommands) | recovered | 0 → 0 | every hook exits 0 with parseable stdout; the live session's store gains no dangling reference |
| hook stdin garbage (six subcommands) | recovered | 0 → 0 | as above |

The stdin rows write three capture sidecars each, with outcome `unavailable` and `published:false`.
That is the design working: a capture that admitted no bytes has no reference to join, and the
sidecar is the durable record of why. §8's calibration rules say how the audit tells that state, and
the permanently unpublished sidecar a PROMPT delivery leaves, apart from the crash it looks like.

## 4. Resources

| case | outcome | dangling | note |
|---|---|---|---|
| disk full | recovered | 0 → 0 | SIMULATED: the `disk-full` injection site, not a real ENOSPC. Run with the daemon STOPPED so the delivery reaches the spool path the site decorates, and the row asserts the faulted delivery did NOT reach the index before concluding anything |
| spool full | recovered | 0 → 0 | SIMULATED: the `spool-full:0` injection site, not a real ENOSPC. Same shape: daemon stopped, faulted delivery proven absent from the index |
| spool read-only | recovered | 0 → 0 | the `spool-readonly` injection site (icacls deny / chmod, from the hook process), daemon stopped |
| `.qompack/objects` read-only | recovered | 0 → 0 | a REAL deny: icacls `(OI)(CI)(WD,AD,WEA,WA,DE,DC)` on Windows, 0o500 elsewhere, proven to bite before anything is concluded |
| lock contention + stale lock | recovered | 0 → 0 | exactly one daemon held the lock; the second `qompack daemon` exited 0 and the holder did not change; hooks delivered during the contention exited 0; a lock naming a dead pid with an aged heartbeat was reclaimed by the next daemon |

The read-only-objects row is worth its own sentence, because the mechanism is not the obvious one:
the denied session's delivery was SPOOLED rather than lost, and lifting the deny let the next daemon
drain it, so the turn reaches the index after the restriction goes away. The row therefore records
`recovered`. Whether anything durable said the restriction was in force WHILE it was in force is
Task 2's F-2, and this row does not re-return it — the degradation line in its record is the
measurement.

## 5. Unavailable historical objects

| case | outcome | dangling | result |
|---|---|---|---|
| object damaged after capture, asked for through `qompack mcp` | failed | 0 → 2 | the answer is a tool error or a miss, not `unavailable` — reproduces S-3 |
| checkpoint written after its pointer's object went away | failed | 0 → 2 | the sealed checkpoint's four pointers all resolve on disk, so it holds no pointer into bytes that are gone — and the row cannot distinguish finalize DROPPING the unresolvable pointer from its never having been a candidate, because no drop entry names the removed object either way. What the row DOES settle is the other half: removing an object under a live index leaves two references that do not resolve and no surface names them. Routed through the standard judge rather than deciding for itself — F4-8 |
| store.Has reports a deleted object as held | failed | 0 → 0 | the predicate `internal/checkpoint` decides pointer resolvability with is blind to a missing file — F4-8 |

Both halves of the §12.3 recovery held in the first row even though the envelope did not: the damaged
object was quarantined to `.qompack/tmp/quarantine/`, and a forced GC pass — negative retention on
both axes, the form `fsck --gc-all` uses — deleted objects while leaving every quarantined file and
every capture sidecar in place.

The middle row is the one that moved this round, and it moved because it was deciding its own outcome
around the rule the other rows obey. It recorded `recovered` with `dangling_after: 2` and `reported:
0`; M16 forbids exactly that, and the row was bypassing it by calling `recordOutcome` instead of the
judge. It now runs one more observation after the seal — so "did the product go on recording" is
measured rather than assumed — and hands the standard judge the same audit pair, the same measured
`recording` and the same named evidence every publication row gets. The judge returns `failed`, and
the finding it pins is F4-8: the store believes it holds bytes it does not, and nothing says so.

## 6. Findings RETURNED, with owning package

Nothing below is fixed in this commit. A finding whose row comes back `failed` is PINNED in the
matrix, so the suite is green today and goes RED if the product changes in either direction —
including if it is FIXED, which is when the pin has to be removed. A finding whose row comes back
`explicit_incomplete` carries no pin: the product does report it, and the finding is about WHICH
surface and HOW MUCH it says.

Every "reported" claim below was taken with the recovery daemon UP and deltaed against a baseline
taken the same way; the surface and the line are named because R4-1 distinguishes them.

### Pinned (the row fails: nothing names the cut on any surface)

| id | finding | owner |
|---|---|---|
| F4-1 | An object whose index line is gone leaves the `tool_use` record and the published capture sidecar pointing at a root nothing can materialize. Recording resumes; no surface names it — the only thing the day log gains is an unrelated segment-tokens warn. | internal/store + internal/daemon |
| F4-4 | A capture sidecar left at stage 1 — a `observe.tool` delivery with `outcome: ok`, `bytes_hash` durable and no reference joined — is exactly the crash `store/capture_sidecar.go` documents, and no surface names it. | internal/store + internal/daemon |
| F4-5 | A truncated `state/retention-roots.jsonl` is not reported on any surface. The consequence is NOT that GC collects nothing: a forced pass over the torn file completed with `RetentionRootsError=false` and deleted every object it scanned. `declaredRetentionLine` (gcrun.go:764-780) retains everything on a line it cannot parse under the blanket `rollback` class, so the cost is over-retention and an unreadable claim — measured in the row's own record. | internal/store |
| F4-8 | `store.Has` answers from an in-memory chunkSet built from `index/roots.jsonl` at Open and never stats a file (read.go:65-75, roots.go:318-323), so an object deleted while its index line survives reads as held. `internal/checkpoint/finalize.go:248-263`'s `toolResultResolvable` decides whether a checkpoint KEEPS a pointer with it, behind a comment claiming resolvability. TWO rows pin it, from opposite sides. `historical_resolvability_blind_to_deleted_object` measures the predicate directly: `store.Has(chunk)=true` with the bytes absent, and the root-level form of the same question — every chunk the root names is held — answering `true` too. `historical_checkpoint_drops_pointer` measures the consequence: after a real PreCompact over a project one object was removed from, two references do not resolve (the root's chunk, and the tool_use record that names that root) and no surface gains anything about either — not LOUD.log, not `status --json` with the daemon up, not `self-test --json`, not the day log, not a DropEntry. | internal/checkpoint (the predicate) + internal/store (the comment on Has, and the index that outlives the object) |
| F4-9 | A checkpoint orphan (artifact with no MANIFEST line), a MANIFEST entry whose artifact is missing, and a MANIFEST digest mismatch each leave a reference that does not resolve, and NO surface names it — not LOUD.log, not `status --json` with the daemon up, not `self-test --json`, not the day log, not a DropEntry. §12.3 says the affected checkpoint is refused and logged Loud; nothing was logged where a reader would find it. | internal/checkpoint |
| S-3 (reproduced) | A damaged historical object reaches the host as a tool error or a `miss` rather than as `unavailable`, so a real address that lost its bytes is indistinguishable from one that never existed (§7.1). Task 3 returned this for quarantined objects; this run reproduces it for damaged ones. | internal/mcp |

### Reported, but weakly (the row is `explicit_incomplete`; the finding is the surface)

| id | finding | owner |
|---|---|---|
| F4-2 | A mid-record truncation of `index/roots.jsonl` also loses the NEXT record appended after it: `paths.AppendJSONL` (appendonly.go:87-109) appends the line and then a newline byte, without checking that the file already ends in one, so the append is glued onto the partial line and both are unparseable. It IS reported — `store: skipped malformed index lines file=roots.jsonl lines=1` at Warn in the day log, and `store.index.badline=1` on the live status snapshot — but only at Warn, only while a daemon is up, and WITHOUT naming which records were lost. Five references dangled and the report says "1 line". | internal/store + internal/paths |
| F4-3 | A mid-record truncation of `index/tool_use.jsonl` is reported the same way: `store: skipped malformed tool_use index lines` at Warn plus the `store.index.badline` counter. Not surfaced in LOUD.log or `self-test --json`, and the lost record is not named. | internal/store |
| F4-6 | An unparseable `.qompack/config.json` stops the daemon from starting; hooks still exit 0 and recording stops. It IS reported — `configuration warning ... unparseable config` at Warn in the day log — but nothing on LOUD.log, `self-test --json` or `status --json`, and the day log is the surface nobody is told to read. Related to but distinct from Task 3's S-7. | internal/cli + internal/config |
| F4-7 | A `state/drain.json` claiming durable bytes the spool no longer has wedges the spool: `validateProgress` refuses it. It IS reported — `daemon: startup drain failed err="daemon: drain: progress no longer matches spool"` at Warn, and `spool_files=5` on the live status snapshot — so `DrainGapProgressUnreadable` does reach a reader, provided the reader asks while a daemon is running. New deliveries are still recorded; the stranded bytes are not replayed. | internal/daemon |
| G-1 | `test/guards/faultenv_test.go`'s `TestGuard_FaultEnvIsConfinedToTwoFiles` is RED at branch tip `fc5228f`, before this commit: `test/platform/platform.go` and `test/security/security.go` name the fault-injection switch in NON-TEST files, two comment lines each. `test/fault` is clean — its evidence vocabulary lives in `evidence_test.go` for exactly this reason. The fix is a comment reword; not taken here because Role D does not own those files. | SP-17 Task 2 + Task 3 (coordinator) |

## 7. The daemon-side seam that does not exist

The injection switch is hook-side by construction: `internal/daemon/spawn.go`'s `buildSpawnEnv`
strips it case-insensitively from every daemon it spawns, and the guard above forbids a second
reader. Nothing in this commit widens it. Every daemon-side forced failure here is therefore one of
exactly two things — a real kill of a real daemon whose lock file lives under a `qompack-fault-`
fixture this package created, or a mutation of files the daemon had already written. That is a
limitation of the coverage and it is recorded as one:

- No row can cut the daemon BETWEEN two steps of one publication. The kill lands where it lands.
  `internal/daemon/delivery_crash_test.go` owns the step-by-step cut in process and stays the
  authority for it.
- "Disk full" here is a forced write failure at a hook-side site, never a real ENOSPC. Every record
  that uses it says so in its `seed_method`.

## 8. What test/fault's audit checks, and which belong in `qompack fsck`

`qompack fsck` does not exist yet (Task 5). The "no newly dangling pointer" check is `auditProject`
in `test/fault/fault.go`. Each class carries its own fsck note in the code; collected:

| audit class | what it resolves | fsck |
|---|---|---|
| `tool_use_root` | every `index/tool_use.jsonl` root, by `finalize.go`'s root-or-chunks rule but resolved ON DISK rather than through `store.Has` | ABSORB — and see F4-8: the product's own copy of this rule asks `Has` and is blind to a deleted object |
| `root_chunk` | every chunk a content record in `index/roots.jsonl` names | ABSORB |
| `delta_base` / `delta_orig` | v2 delta pointers | ABSORB |
| `index_bad_line` | line-by-line parse of both index files, NAMING each bad line | ABSORB — `loadRoots` collapses these into two counters, so fsck needs its own scan |
| `capture_unpublished` | a sidecar for an `observe.tool` delivery with outcome `ok` and `bytes_hash` durable, never joined | ABSORB (report; never repair — sidecars are evidence) |
| `capture_bytes_absent` | a published sidecar whose root no longer resolves | ABSORB |
| `manifest_artifact` | `checkpoint.Reader.Verify` over every manifest entry | ABSORB — the interface already names this as fsck's |
| `orphan_artifact` | an artifact with no MANIFEST line | ABSORB (re-hash and REPORT; `paths.AppendManifest` is the only legal writer, so appending is a repair) |
| `checkpoint_pointer` | every file and tool pointer a verifying checkpoint carries into the store | ABSORB |
| `retention_root` | every hash a producer asked GC to retain. NOTE the calibration: an `evidence`-class root names a capture sidecar's `bytes_hash`, which lives under `records/captures/` and never in `objects/` — resolving it against the object store reports every ordinary capture as dangling | ABSORB, with that distinction built in |
| `drain_gap` | `state/drain.json` against the spool it describes, in `DrainGapKind`'s vocabulary | PARTIAL — "unreplayed spool bytes" belongs in `doctor`; what fsck should absorb is the REFUSAL case, a progress document that disagrees with its spool, because that one wedges recording until someone looks |
| `journal_unreadable` | the delivery lease and ack journals still parse | ABSORB, with `delivery_seal_tool.go`'s discipline: report, take the daemon lock before any write, never edit a journal — only the derived position files |

Five calibration rules Task 5 should inherit, every one of them learned by measuring rather than
reasoning, and four of them after shipping the wrong version first:

1. **Resolve chunks on disk, never through `store.Has`.** `FSStore.Has` answers from an in-memory
   chunkSet built from `index/roots.jsonl` at Open and never stats a file, so an object deleted while
   its index line survives reads as held. An audit that asks `Has` reports a clean store for a store
   that has lost its bytes — and every row that depends on that audit becomes vacuous. That is F4-8
   for `internal/checkpoint`, and it was this package's own first defect.
2. **`published:false` on a capture sidecar is a gap only for an `observe.tool` delivery with outcome
   `ok` and `bytes_hash` durable.** A capture whose outcome is not `ok` admitted no bytes and has no
   reference to join. A PROMPT delivery admits bytes and produces no `ToolUseRecord`, so it is
   permanently unpublished by design. And keying on `root` instead finds nothing at all: `root` is
   written only by `LinkCaptureReference`, which sets `published: true` in the same assignment.
3. **An `evidence`-class retention root does not name an object.** It names a capture sidecar's
   `bytes_hash`, which lives under `records/captures/`; resolving it against `objects/` reports every
   ordinary capture as dangling.
4. **A torn `retention-roots.jsonl` does not stop collection.** `declaredRetentionLine` retains
   everything on a line it cannot parse under the blanket `rollback` class; only an in-process
   source's failure raises `errRetentionRootsUnavailable`. A repair tool that assumed the pass had
   stopped would be reasoning about the wrong failure.
5. **"The product reported it" is a claim about a TOKEN, and the token must be one only the product's
   report can carry.** Three failed that test here. `ack` is a substring of "qompack", which prefixes
   every line the day-log reader returns and opens most of the product's own error strings, so one
   row's filter matched the entire log. `lease` is a substring of "release", `seq` of "sequence" and
   "consequence". And the session id in a line is derived from the row's own name, so a row can match
   itself. What a filter must NOT do is throw away the file a `file=` value names: that is the
   product naming the artefact it stepped over, and it is the strongest thing a torn-index row gets.
   The rule that came out of it: strip the harness's own identity (the log file name, the session id,
   the fixture directory) and keep everything the product chose to say, base names included.

## 9. What stayed unverified

- **No row cuts the daemon between two steps of one publication.** §7 gives the reason.
- **No real ENOSPC.** §7.
- **Checkpoint coverage is over a synthesized artifact on this host.** The artifact is written
  through the product's own writers, so the manifest, digest and immutability are the real ones; what
  is not exercised is a cadence-sealed checkpoint's own pointer set.
- **One platform.** windows/amd64 only. Nothing here claims anything about the other five targets.
- **Whether a gap is visible to a user depends on whether a daemon is running when they look.** The
  counters that name a degradation live in `StatusReport.Snapshot`, which is the daemon's; with the
  daemon stopped the document does not carry them. Every row here now asks with the daemon up, so the
  rows say what a user WOULD see at the best moment — not what they see after the session ends.
- **A day-log Warn is the weakest surface that still counts.** F4-2, F4-3, F4-6 and F4-7 are reported
  there and nowhere stronger, and nothing tells a user to read that file. R4-1 admits it as explicit;
  Task 7's docs should decide whether that is enough.

# Commit 4 — proposed recovery prose

Role D PROPOSES; the coordinator folds this into `docs/security.md` and `docs/release.md`. Every
claim below is backed by a record in `commit4-fault-windows-amd64/` and by a row of
`commit4-evidence.md`; nothing here is inferred from the code without having been run.

## What recovers on its own

These need no operator. They were each cut on the installed bundle and came back.

- **A killed daemon.** Deliveries in flight are spooled, and the next session-start brings a new
  daemon up over that spool and replays it. Nothing is lost and nothing is left dangling.
- **A crash during a session.** The next session-start resumes recording into the same project.
- **A read-only `.qompack/objects`.** Deliveries made while the directory is unwritable are spooled
  rather than dropped; when the restriction is lifted, the next daemon drains them and the turns
  reach the index. The same holds for a spool that cannot be written or that refuses every append.
- **Two daemons for one project.** Exactly one holds the lock; the second exits 0 without touching
  the project. Hooks delivered during the contention exit 0.
- **A lock left behind by a daemon that died.** It is reclaimed once its heartbeat goes stale. The
  window is `internal/daemon`'s `staleAfter` (90 seconds today), and during it recording does not
  resume — a hook still exits 0, and the events go to the spool for the daemon that eventually wins
  the lock. Worth saying in the docs plainly, because "qompack stopped recording for a minute and a
  half after a crash" is otherwise indistinguishable from a fault.
- **A torn delivery seal, including a half-present pair.** The journals are untouched by it and the
  next daemon rebuilds the derived position files.
- **A corrupt `run/state.bin`.** Rewritten from scratch on the next run.
- **An MCP server killed mid-request.** The host sees the session end. The archive is unchanged: a
  retrieval server is a reader, and a killed reader cannot leave the store different from how it
  found it.
- **Every malformed lifecycle sequence we could construct** — a duplicate tool call, a duplicate
  PreCompact, a compaction whose summary never arrived, a compaction whose SessionStart landed under
  another session's id, a turn the host never delivered, a PreCompact before the events it would
  summarise, a SessionEnd before its Stop. Hooks exit 0, the index gains no duplicate record, and no
  reference dangles.

## What stays explicitly incomplete

These do NOT heal, and the product says so. That is the designed answer, not a defect: some state
genuinely cannot be reconstructed, and §13 invariant 10 requires the incompleteness to be loud rather
than silent.

- **A checkpoint whose artifact is missing, or whose bytes no longer match its MANIFEST digest.** The
  affected checkpoint is refused and its parent is used instead; the artifact is never repaired in
  place. `qompack fsck` (Task 5) is where reconciliation belongs. What must NOT be said in the docs
  is that a user is told — or rather, that WAS what must not be said: this matrix cut all three
  checkpoint failures (orphan artifact, manifest entry without its artifact, digest mismatch) and
  none of them reached LOUD.log, `status --json`, `self-test --json`, the day log or a drop entry.
  That was F4-9. **Commit 6 fixed it**: `List` sweeps the directory, `Verify` is no longer silent
  about a digest mismatch, and the daemon runs both once at startup, so a recovery session now Louds
  each defect by artifact name. The pins are gone and all three rows are `explicit_incomplete`. The
  docs may say the user is told; they still may not say anything is repaired.
- **Spool bytes a drain has not replayed.** They are named as a pending gap rather than assumed
  delivered. A truncated WAL segment or client spool file leaves the record that was torn behind and
  the rest is replayed.
- **A capture whose bytes were never admitted** — denied, unavailable, corrupt. A sidecar under
  `records/captures/` records the outcome and why. These are evidence and are never swept: a forced
  garbage collection with retention disabled on both axes leaves every one of them, and every
  quarantined object, in place.

## What needs an operator

Today these need a person, and until `qompack fsck` ships most of them need a person with a text
editor. They are listed in the order a session notices them.

0. **A damaged checkpoint tier.** An artifact with no manifest line, a manifest line whose artifact is
   gone, or an artifact that no longer matches its digest. The reader refuses the affected checkpoint
   and falls back to its parent, so a session degrades rather than breaks — but nothing tells anyone,
   so the only way to find it today is to look. `qompack fsck` re-hashing the tier is the answer.

1. **A corrupt `.qompack/config.json`.** The daemon will not start. Hooks keep exiting 0 and
   recording has stopped. Repair or delete the file; the defaults are reloaded on the next run. The
   It is reported on LOUD.log since **commit 6 fixed F4-6**: a configuration layer that does not
   parse is a keyless warning, and `LoadConfigAndReport` Louds those rather than filing them at Warn
   in the day log, where they were the only trace. `self-test --json` and `status --json` still do
   not carry it.
2. **A torn index line.** `index/roots.jsonl` and `index/tool_use.jsonl` are append-only logs written
   by `store.(*appendFile).write` on a handle from `openAppendFile`, not by `paths.AppendJSONL`. A
   line torn by a crash costs that record. Commit 6's choke-point fix writes one newline when that
   handle opens if the file is non-empty and does not already end in one, so the next record is its
   own line. The store reports the torn line (`store: skipped malformed index lines` at Warn, and a
   `store.index.badline` counter on a live status snapshot) but reports a LINE COUNT, never which
   records were lost. Truncating the file back to its last complete newline recovers everything
   below it. fsck should do this as an explicit repair, and should name the records.
3. **A stale `state/drain.json`.** A progress document that claims durable bytes the spool no longer
   has wedges the spool: the drain refuses to consume it. This one IS reported where an operator can
   find it — `daemon: startup drain failed ... progress no longer matches spool` at Warn, and a
   non-zero `spool_files` on `qompack status --json` — provided the status is taken while a daemon is
   running. New deliveries are still recorded; the stranded ones are not replayed until the document
   is corrected. The document is DERIVED state, so
   deleting it is safe and is the right repair; the journals it summarises are not.
4. **A truncated `state/retention-roots.jsonl`.** Collection is NOT stopped by it — a forced pass
   over a torn file completed and deleted every object it scanned. What happens instead is that the
   line nobody can parse is treated as a claim nobody may ignore: everything on it is retained under
   a blanket `rollback` label. So the cost is retention of material nothing can account for, and no
   surface says the file is torn. Truncating back to the last complete line restores the file; the
   claims the torn line carried are gone either way.
5. **A stage-1 capture sidecar** — an `observe.tool` delivery whose sidecar carries `outcome: ok`
   and a durable `bytes_hash` with no reference joined. (A sidecar that is unpublished for any other
   reason is not this: a capture that admitted no bytes never had a reference to join, and a PROMPT
   delivery produces no tool record and so is permanently unpublished by design.) The bytes are
   durable and no reference was ever joined. This is a REPORTABLE state, never a repairable one: the sidecar is the
   evidence that a capture happened, and a tool that "fixed" it by inventing a reference, or by
   deleting the sidecar, would destroy the only record that it happened at all.
6. **A damaged or missing historical object.** It is quarantined to `.qompack/tmp/quarantine/` on
   first read and never collected. Restoring it needs a backup; there is no reconstruction.
   Retrieval for that address should say `unavailable` — see the limitation below.
7. **An object REMOVED while its index line survives.** This is the same damage as above with the
   quarantine path never taken, and it is worse, because nothing reads the object and so nothing
   notices. A checkpoint sealed afterwards is not the problem — its pointers were measured and every
   one of them resolved on disk — but the index that outlived the object still names it: a root that
   names a chunk `objects/` does not hold, and a `tool_use` record pointing at that root. Neither
   reaches LOUD.log, `status --json` with a daemon up, `self-test --json`, the day log or a drop
   entry. **Commit 6 fixed the half of this that was F4-8**: `internal/checkpoint` decides whether to
   keep a pointer with `store.ObjectPresence.ObjectOnDisk`, which stats, rather than with `store.Has`,
   which answers from the index — so a checkpoint no longer makes a durable promise about bytes that
   are gone. `store.Has` is unchanged and still answers from the index; that is its documented
   contract and it is the cheap question. What survives is F4-1, ruled out of commit 6's scope: the
   INDEX goes on naming bytes that are gone and no surface says so. `qompack fsck` (Task 5)
   re-resolves references against disk and is the surface for it; short of that, the only repair is a
   backup and the only detection is to look.

## Limitations to state in the docs rather than leave to discovery

- **Fault injection is hook-side only.** The switch `internal/cli/fault.go` reads is stripped from
  every daemon the product spawns, and a guard keeps it to two files. So no test, including this one,
  can force a failure BETWEEN two steps of one daemon-side publication. Daemon-side cuts in this
  matrix are real kills or file mutations, and "disk full" in these records is a forced write failure
  at a hook-side site, never a real ENOSPC. Anything a release note says about disk-exhaustion
  behaviour rests on a simulation and should say so.
- **Whether a gap is visible depends on whether a daemon is running when you look.** The counters
  that name a degradation live in the daemon's status snapshot, so `qompack status --json` run after
  a session has ended does not carry them at all. The docs should say: ask while the session is live,
  or ask `qompack fsck` once it ships.
- **The day log is the weakest surface that still counts, and nothing points a user at it.** A torn
  index, an unparseable config and a wedged spool are each reported there at Warn and nowhere
  stronger. If the docs are going to claim degradation is loud, they should name the file
  (`.qompack/logs/qompack-<day>.log`) and say that LOUD.log is not where these land.
- **Four states are reported nowhere at all today**: an object whose index line went missing, an
  object whose BYTES went missing while its index line survived, a stage-1 capture sidecar, and every
  one of the three checkpoint-tier failures. The docs must not promise the user will be told about
  these.
- **An address that lost its bytes currently reads like an address that never existed.** Retrieval
  answers a damaged object with a tool error or a miss rather than with `unavailable`. Until that is
  fixed, `docs/security.md` should not claim the distinction holds on the retrieval path; the
  distinction is real in the store and in the capture evidence.

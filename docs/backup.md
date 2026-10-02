# Backup and restore

The operator commands require a stopped source daemon and an existing project store. They acquire
the same writer lease used by the packaged daemon before opening the store, and hold it through
store closure. A live writer causes refusal. Import and cutover remain behind their existing build
gate; backup does not enable either.

Use the binary from the candidate you intend to validate. Give each attempt a new backup ID and
keep failed attempts for inspection:

```text
qompack backup create --project <source-project> --id before-change --json
qompack backup verify --project <source-project> --id before-change --json
qompack backup restore --project <source-project> --id before-change --destination <recovery-project> --json
```

All three commands also refuse, exit 1, with `backup: resolve configuration violations and warnings
before maintenance` while the source project's configuration does not load exactly as written:
whenever `qompack self-test`'s `config.capture` row is not `ok`. Maintenance runs only on the
configuration as written (`internal/cli/backup.go`). The case that most often hits this is a plugin
downgrade: a config file written by a newer build declares a `settingsVersion` this build does not
understand, and its block is reset to defaults. Take the backup with the newer build before
downgrading, or, after it, set the newer block aside and back up then, as
[troubleshooting §6](troubleshooting.md#6-configuration-and-schema-compatibility) ("After a plugin
downgrade or upgrade") describes.

Restore requires the destination's `.qompack` to be absent. It stages on that filesystem, validates
the manifest and copied bytes, reads content with the same build, and publishes without replacing
an existing destination. Unsupported no-replace filesystem operations fail closed. The command
then runs the packaged integrity checks, including the delivery-seal check. An unsuccessful
integrity check returns failure and retains the destination for inspection.

Verify judges a backup the way restore does. It re-hashes every file the manifest names, then
restores the backup into a scratch destination, `.qompack/tmp/verify-<id>-<random>/project` inside
the source project, makes the same reader proof and runs the same integrity checks, and reports
both. A scratch restore that passes is removed; one that fails is kept, and the error names where.
A backup that verifies therefore restores, and one that restore would refuse does not verify. The
scratch copy stays inside the source's `.qompack/`, on the same filesystem as the store and outside
every backup; verify writes nothing to the system temporary directory, changes nothing in the
backup, and changes nothing in the source store outside `tmp/` and the daemon lock it holds while it
runs. Delete a kept scratch restore once you have inspected it.

The reader proof reads every content root and every tool reference back. A tool reference whose
root a garbage-collection tombstone retired (typically the MCP server's own ephemeral answer, which
the daemon collects after an idle period) is counted in `ToolRefsTombstoned` rather than read, which
is how `qompack fsck` reads the same reference; a reference nothing accounts for fails the restore.
The proof itself does not read checkpoint chains or delivery seals (`CheckpointSealCovered` stays
false). The integrity report that follows it does, and the response's note says whether that report
ran: a restore refused before it published a destination says that no integrity check ran.

The JSON response reports the manifest or reader proof, integrity results and any error. Exit codes
are 0 for success, 1 for an operational failure and 2 for invalid arguments. A restore proves only
the checks it reports; it is not evidence that an older release can read newer data.

A store written by an earlier build reads as follows:

- A store the public v0.2.0 release wrote passes `fsck`, backs up, verifies and restores with this
  build. A committed store written by that release's own hooks and daemon keeps this checked. v0.2.0
  writes no capture sidecars, checkpoints or drafts.
- Development builds before the prompt link (up to 0.2.99-prev) published every prompt as its
  `prompt_<session>_<turn>` record but left its capture sidecar unlinked. Such a sidecar is read as
  published when a prompt record in its session matches its prompt, one record per sidecar, and
  that record comes before the first prompt this build published in the session (this build's own
  prompts carry an observation record in `index/observations.jsonl`; a record from then on is this
  build's and accounts for no earlier sidecar). `fsck`'s `captures` and `publication` rows name how
  many there were. Nothing is rewritten.
- Builds before the V6 close-out recorded an idle-time segment encode before any checkpoint sealed
  it. After an idle exit, `index/segments.jsonl` could then name a checkpoint that was never written.
  `fsck`'s `index.segments` row names such a record as an `unsealed-draft claim` when the draft's
  own state file explains it. The session's next compaction seals a pending draft at that number. A
  draft that was set aside is sealed by nothing, and the segment's turns stay readable from the
  capture log either way. A claim nothing on disk explains is still a defect. This build records a
  segment's encode only when the checkpoint that holds it is sealed.

Copy a project to another path with `backup` and `restore`, not by hand. A backup leaves out
`.qompack/run/`. A hand copy carries the original's `run/daemon.lock`, which records the original
project's root (or, from an earlier build, an address named for the original project). This build
judges such a lock by the copied store's own heartbeat. It is reclaimed once that heartbeat is older
than the 90-second staleness window; a copy that gave `run/daemon.hb` a fresh modification time
waits that long. Until then each daemon start logs `this project's lock was written for another
project path` and exits, and `fsck`'s `daemon` row names the lock.

The source remains the source. Restore does not switch the active project, delete later writes or
automatically downgrade a schema. Preserve later writes in the original tree. Before activating a
recovered project, stop incompatible writers, retain the original store and backup, and validate
the intended reader and project-file snapshot together. Human UAT and cross-version compatibility
remain separate release gates.

Startup publication accounting reports unpublished successful tool, prompt and subagent captures and unindexed object
candidates in status counters and LOUD diagnostics. Counts from capped, interrupted or unreadable
scans are lower bounds. This discovers incomplete state; it does not reconstruct missing content
or replay captures under an assumed permission grant. Use `qompack fsck --project <root> --json`
for an operator audit and preserve its failures before choosing a verified backup.

A backup with a `.certification-pending` marker was not confirmed under the writer lease and cannot
be verified or restored by these commands. Preserve it for diagnosis; create a new backup with a
fresh ID after resolving the writer problem. Deleting the marker is not a supported repair.

The delivery journals rotate instead of stopping. Each journal file keeps its 65,536-entry and
64 MiB bound; when either is reached the journal archives its whole window into the generation store
(`.qompack/state/delivery-generations/`), opens a fresh segment under
`.qompack/state/delivery-segments/<n>/`, and records the switch in the segment authority
(`delivery-journal.json` and `delivery-journal-log.jsonl`). Every retired delivery keeps its
observation identity and its acknowledgement, arrivals continue densely, and a late copy of an
archived delivery is still recognised. A backup copies all of it — the original four journal files,
every segment, the authority and the generation store — and refuses a copy if any of them moves under
it; a restore runs the offline check over every segment.

The first rotation freezes the seals of the original four files (`delivery-lease-position.json`,
`delivery-ack-position.json`), so a build that predates rotation refuses the journal rather than
appending to it: its `fsck` and `admin delivery-seal --check` report that the seals carry no version,
and its daemon leases and acknowledges nothing and changes no delivery-state file. A build from
before the V6 fail-closed journal change still indexes what it receives in that state, without
observation identities (its degraded mode for an unavailable journal), so those captures carry no
redelivery protection; the current build does not revisit them, and resumes each session at the
arrival it left next. Roll back past a rotation only by restoring a backup taken before it.

Nothing stops the daemon before the first rotation: it happens on its own when the active journal
reaches 65,536 entries. If you may want to run an older build on a project again, take a backup
before then. The daemon warns once per run when a project that has never rotated reaches three
quarters of the way (49,152 entries or 48 MiB): the daemon log says `this project's delivery journal
will rotate for the first time soon`, the `delivery_first_rotation_backup_advised` counter counts it,
and `qompack doctor`'s `delivery.rollover` row says whether the store has rotated and whether the
last daemon warned. That Warn is the prompt to stop the daemon and run `qompack backup create`; a
backup taken after the rotation cannot take the project back to an older build.
`qompack fsck --seal-check` (or `qompack admin delivery-seal --check` on a stopped project) reports
how many entries the active lease journal holds, and a store that has already rotated has a
`.qompack/state/delivery-segments/` directory. Do not delete, rename or
rewrite journals, seals or segments: manual deletion can recycle observation identities and is not a
recovery procedure. The delivery state grows with the project's delivery history and is never
pruned: about 0.18 GiB per 100,000 deliveries in the V6 close-out's measurements, most of it the
generation store (`plans/V2-WAVE1-carried-defects.md`, SP20-D4).

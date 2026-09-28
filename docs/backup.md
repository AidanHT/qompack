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

Restore requires the destination's `.qompack` to be absent. It stages on that filesystem, validates
the manifest and copied bytes, reads content with the same build, and publishes without replacing
an existing destination. Unsupported no-replace filesystem operations fail closed. The command
then runs the packaged integrity checks, including the delivery-seal check. An unsuccessful
integrity check returns failure and retains the destination for inspection.

The JSON response reports the manifest or reader proof, integrity results and any error. Exit codes
are 0 for success, 1 for an operational failure and 2 for invalid arguments. A restore proves only
the checks it reports; it is not evidence that an older release can read newer data.

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

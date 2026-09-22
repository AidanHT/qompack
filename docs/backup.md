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

The current delivery journal has a finite 65,536-entry and 64 MiB admission bound. Once a bound is
reached, new identified deliveries remain pending in durable input and are reported unavailable;
they must not be processed as identity-free replacements. Existing completed identities remain
readable. Automatic journal rollover is not yet enabled or release-verified: preserve the journals and pending input,
stop recording, and retain a consistent backup before planning an engine-compatible migration.
Manual journal deletion can recycle observation identities and is not a recovery procedure.

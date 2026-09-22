# Backup frontier correction

2026-09-22. Coordinator resolution of the earlier backup-watch handoffs and
`segment-authority-independent-review.md`. Their claim that comparing copied
bytes with a post-copy read suffices regardless of copy order was rejected.

`TestBackup_RefusesFrontierChangedBeforeItsOwnCopy` changes the generation head
immediately before that file's own copy. The original reader accepted the backup
even though earlier files could belong to the prior generation. Preserved run
`backup-precopy-frontier-negative-control` fails at the actual missing refusal.

The corrected reader hashes watched state before any content copy, then requires
presence, size and digest to agree with captured and post-copy bytes. The fixed
list preserves four legacy files and adds the generation manifest/head and segment
transition log/head. Mutable journals/seals in each discovered segment are also
watched. Segment discovery uses bounded directory batches and rejects aliases,
unreadable paths and unexpected nesting; file hashing streams through the Windows
shared-delete reader with context checks and opened-identity validation.

Evidence:

- `backup-whole-copy-frontier-corrected`: compile failure while the journal author
  had not yet defined `rolloverArmed`; no backup pass inferred.
- `backup-precopy-and-transition-log`: focused backup refusal/restore and actual
  shared-reader guard tests pass; source hash unchanged during this run.
- `backup-store-scoped-lint` and `backup-store-nomagic`: pass.

The retained watched-set test now checks eight exact, distinct fixed names and
copy isolation. Its four original members remain. The shared-reader AST guard
now checks the actual opening helper, `backupFileDigest`, plus its own scanner
negative control. No test or baseline was discarded.

This check relies on the journal's monotonic writer protocol. It does not prove
arbitrary ABA edits impossible, immutable-page closure, old-reader compatibility,
or final packaged restore. The maintenance CLI requires the daemon writer lease;
final segment recovery, rollback and installed-package gates remain outstanding.

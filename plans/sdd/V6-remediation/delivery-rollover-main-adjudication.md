# Coordinator adjudication of revised rollover review

2026-09-21. This is design/integration direction, not evidence of a working migration.

Use immutable generations and exact bounded on-disk membership. Legacy files remain anchored;
never rename away the last recovery anchor. Keep old writer incompatibility explicit. A legacy
binary's loader rejection does not halt its fallback writer, so no old product compatibility is
certified. Writer enablement requires new-reader support and an operator stop/backup boundary.

Do not accept a hash mismatch as an insertable novel nonce when the keyed hash collides: return
unavailable rather than overwrite or forget either original key. Leaf keys are checked in full.

Archived ACK validity is checked per lookup by resolving the nonce's original lease and comparing
all identity fields. A root's content hash proves byte integrity, not semantic correctness of an
unread join. Archived complete-frontier summaries need a verified construction and explicit
limits; do not accept an unchecked joinOK boolean as proof of semantic recovery.

The sidecar review's claim that one appendFile.write prevents a record-without-marks power-loss
state is rejected. A single regular-file Write prevents interleaving under its mutex, not arbitrary
partial persistence. Keep the single-write batch on the normal path, plus an fsynced intent and
precondition-guarded recovery for actual crash cuts. Later supersession must not be overwritten.
Pure lookup must never expose uncommitted intent as a record. These are the existing superseding
sidecar instructions, not new authorization to change frozen record shapes.

# V6 reader integration — coordinator record, 2026-09-22

This records implementation progress, not release sign-off. Rollover remains
default-off; the original V6 report and its artifacts remain immutable.

## Actual writer agreement and restart correction

`delivery-reader-producer-integrated` used real daemon journal bytes for GC and
backup/restore. GC retained active and archived pending payloads, collected settled
and unrelated controls, and refused collection when required archived files were
missing. Backup passed offline integrity but reopening the restored writer failed:
`loadAcksFrom` searched only the active lease map for an archived acknowledgement.
The original failure remains in that run.

Startup now resolves absent active-map entries through the generation store and
propagates lookup failures. It still requires the acknowledgement's exact original
observation identity. `delivery-reader-producer-archived-ack-corrected` passes the
affected reader, path, generation, authority and acknowledgement tests, including
restored identity reuse, existing ACK state, original payload bytes and arrival 4
after restore. This proves same-build recovery; it does not prove an older binary
can read new-format writes.

`delivery-seventy-rotation-retention` passes the retained live rotation, restart
and archived ACK/frontier cases. The identity case now also checks GC retention of
the oldest pending payload after at least 70 rotations. This is functional evidence,
not a latency benchmark or a bound on total history size.

## GC identity and seal validation

Only a genuinely absent observation field selects the historical nonce-only lease
format. Explicit null/empty IDs, zero hashes, bare hashes and uppercase alternatives
cannot settle a lease. ACK identities must be canonical and nonzero. The corrected
focused run `gc-ack-identity-corrected` passes these cases and both retained legacy
compatibility cases.

The run named `gc-ack-identity-negative-control` is **not a valid negative control**:
source changed while its compiler was running, and it returned PASS on corrected
behavior. Both source hash inventories and its log remain. No pre-fix failure is
inferred from that misleading run name.

The GC reader's initial seal check accepted arbitrary nonempty bytes. The coordinator
returned that handoff for supported v1/v2 seal-format validation. Its focused
correction, actual writer agreement and Linux validation must be inspected together;
nonempty file size alone cannot certify a seal.

## Independent compatibility review disposition

The independent review correctly identifies that old segment-unaware binaries ignore
the new authority files. A v2 seal cannot exclude a v2-capable old reader. Its proposed
new seal marker would establish journal-loader refusal only: prior coordinator
review already found legacy fallback paths can still write other state after that
loader fails. No universal old-product refusal or automatic downgrade is certified.
Do not change the frozen evidence schema or enable rollover on that proposal alone.

The review's illustrative paths are inaccurate. Actual anchors are
`delivery-journal.json`, `delivery-journal-log.jsonl`,
`delivery-lease-position.json`, and `delivery-ack-position.json`. The source and
actual writer tests determine those names. The default-off guard conservatively
refuses surviving migration evidence, including an empty authority log; relaxing
that refusal requires a separate recovery proof, not a claimed successful migration.

Before enabling a new writer: stop incompatible processes, take and verify a
consistent backup, exercise the intended old-reader boundary and rollback before
and after new writes, retain later writes, and measure the new implementation's
resource costs. Those acceptance gates remain open. SP20-D4 is not marked fixed.

## Subsequent Linux and negative-control evidence

`linux-readers-race-corrected` passes the real producer, offline-reader, GC,
identity, seal-format and Linux FIFO/alias cases. `linux-generation-race-focused`
passes radix, generation, wiring and archived-terminal cases. The immutable
archive and executed source are identified in `linux-readers-executed-source.json`;
they do not certify a different installed bundle. The first reader invocation's
CRLF argument failure remains recorded separately.

`linux-gc-ack-isolated-negative-control` deliberately bypassed both observation
identity guards in a separate frozen copy. All five invalid-identity cases then
failed by allowing collection of the retained root, as expected. The candidate
was not modified. `gc-ack-isolated-negative-control.json` records the original and
replacement hashes. This provides the missing negative-control evidence without
relabeling the earlier invalid invocation as a failure.

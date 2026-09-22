# Coordinator decision: segmented delivery retention

2026-09-21. Implementation remains authorized; release acceptance is pending.
The default-off generation helper is not capacity closure. The coordinator owns
the shared retention and rollback contract; daemon authors may implement it within
the exclusive source assignments below.

Use additive journal segments and preserve the four original legacy journal/seal
files as the original segment. Do not rewrite frozen lease/ACK records, rebase
arrival numbers, discard a pending lease, or rename away original anchors.
New segments live under `state/delivery-segments/` in canonical zero-padded
20-digit unsigned sequence directories, starting at 1. Each contains the same
four journal/seal filenames. `state/delivery-journal.json` is the versioned
active-segment authority. It must identify the active segment and the immutable
generation root preceding that segment; exact wire and crash ordering require
coordinator review before enablement. Unknown/missing/conflicting authority or
missing referenced files is unavailable, never permission to recreate history.

The persistent radix remains the full nonce/arrival/ACK/terminal lookup. The
active journal holds a bounded window. A new segment's reader starts each
session from its exact predecessor root, so dense global arrival identities
remain unchanged without altering legacy wire records. ACKs for an archived
lease must resolve and compare its original binding. Ordering consults the
per-session unsettled frontier. Terminal dispositions remain distinct from ACKs.

Rotation must exclude both journal pipelines while allowing their normal
independent commits. It must drain admitted work, durably reconcile the outgoing
segment, create/sync the new segment and publish a single clear active authority
before assigning there. Preserve complete and partial attempts as evidence;
never silently overwrite a staged conflicting segment. Ownership loss, close,
cancellation and every uncertain I/O boundary must refuse further assignments.
Do not hold the owner lock while waiting for an operation that requires it.

Store GC will read legacy and segmented lease roots, with exact ACK identity
checks. The coordinator owns this reader addition. Enumeration/read failures,
unknown authority, missing referenced segment or scan budget exhaustion must
prevent collection. Limited ACK lookup may over-retain and must be identified;
it may never release a lease on a nonce-only or unproved join. No directory glob
with an unreadable result may become an empty retention set. Staged uncommitted
files may conservatively retain; they do not authorize a delivery or identity.

Backup must watch active authority, generation manifest/head and every copied
mutable segment journal/seal. Per-file atomicity does not certify a consistent
multi-file snapshot. The coordinator owns this additional backup integration.
Actual tests must cross the capacity seam through live journal APIs, restart,
replay the oldest nonce, keep dormant-session continuity and retain an oldest
unsettled lease across at least 70 segment transitions. Preserve original failures.

Old writers must be stopped before cutover. No statement here makes an old binary
recognize the new authority. Before/after-new-write rollback requires an isolated,
verified backup/reader rehearsal and explicit disposition of later writes.
Generation pages and identity history grow on disk; bounded active memory and
lookup work are not a claim of bounded total lifetime storage.

## Review of the author's first authority proposal

The proposed `v`, `active`, `prev`, `base_root`, `chain` fields are an initial
shape, not approval to enable. Two corrections are required before acceptance:

1. Absence cannot unconditionally mean legacy when segmented/generation evidence
   survives. Losing the authority after new writes must not reopen the frozen
   legacy segment and mint duplicate arrivals. Distinguish a genuinely unmigrated
   tree from interrupted staging or lost authority. One supported approach is to
   commit an initial active-0 authority before staging the first segment; any
   surviving migration evidence with missing authority then refuses open. Never
   silently reconstruct active history from directory ordering.
2. A chain depending on `prevChain` cannot be validated from a single overwritten
   head that does not retain its predecessor. Preserve an immutable authority
   record with each segment (or an equivalent authenticated, versioned transition
   log) before publishing the active pointer. Define how the reader validates the
   active record, predecessor/root binding and staging state without trusting a
   bare self-reported chain. A mutable head alone is not a historical chain.

Actual authority wire remains under review. The store retention reader will not
guess incomplete fields or certify an unknown schema. These requirements apply
even if the implementation seam is enabled in tests only.

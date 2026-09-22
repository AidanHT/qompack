# Coordinator: terminal denied replay is distinct from publication

2026-09-21. A previously leased delivery can become denied on replay (for example its path now
resolves through an escaping symlink). Skipping its WAL bytes without accounting for the old lease
leaves every later arrival blocked. A capture ACK would falsely certify a denied/unavailable event.

Add a versioned, payload-free terminal replay disposition under state/delivery-terminal/, keyed by
ObservationID and bound to the full existing lease and a closed reason code. It means only that
replay is retired by a proven policy denial. It does not claim capture, restore, task completion,
original host ordering or an absent object. ACK records stay separate and untouched. If historical
publication and later denial both exist, both facts remain; this is not a data-deletion mechanism.

Only explicit Denied may write this disposition. An unprovable/failed policy remains unavailable
and pending. The request's session, nonce and request hash must match the existing lease. No fresh
lease or observation can be created by retirement, and no denied payload/path/prose is persisted.
A verified terminal disposition allows ordering to pass that arrival, while status/gap evidence
continues to classify it denied. Write/fsync the disposition before consuming the source offset.

Per-observation atomic files avoid partial append ambiguity. Missing disposition is conservative
pending, not successful completion; unreadable or conflicting evidence is unavailable. Bound record
size and startup enumeration to the existing lease bound. No old-writer compatibility is inferred.
Main owns the new helper, journal loading and terminal map; the ordering author owns its gate/drain
files until handoff, then main wires terminal lookup/retirement and runs the integration cuts.
The capacity migration must fold/resolve terminal dispositions alongside leases and ACKs.

Integration adjudication: retireDenied is called outside owner.mu; it takes that
lock itself. Orphaned/conflicting dispositions remain unavailable and preserved;
there is no automatic fsck clearing. Recovery requires the original matching lease
history or a verified backup. Terminal writes now stage/rename through os.Root,
then verify the pinned directory still names the store before directory syncing.

Retirement is wired into drain before offset advancement and consulted by live
workers and replay before capture. Failed policy or unavailable identity stays
pending. Existing ACK evidence remains distinct. The retained provisional
TestDeliveryOrder_LeasedDenyOnRetryPreservedPending now asserts this durable denial
retirement instead of the formerly documented immortal predecessor; new restart
and actual physical-scope-transition tests cover the integrated boundary.

Publication-time scope is rechecked for queued live work and for replay after blob
resolution, including deferred look-ahead requests. Drain retirement obtains the
shared in-flight delivery key so it cannot overtake a currently running capture
handler. The key is released without claiming capture completion. A permitted
historical publication may coexist with later denial; the latter is not deletion.

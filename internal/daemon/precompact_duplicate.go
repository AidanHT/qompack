package daemon

import "slices"

// The claim handleCheckpoint makes on each PreCompact it seals, so a hook's spooled copy of one is
// not sealed again (V6 close-out audit 2, finding 4; handleCheckpoint says why).

// maxSealedPreCompacts bounds daemon.sealedPreCompacts. A spooled copy of a PreCompact reaches a
// drain within that compaction's aftermath (the client-spool watcher this route kicks, the idle
// drain, Stop's drain), long before eight more compactions have been sealed; one older than that is
// sealed again, as every copy was before the claim existed.
const maxSealedPreCompacts = 8

// sealClaimedLocked reports whether a route of this daemon has claimed the PreCompact delivered under
// nonce (claimSealLocked). A request with no nonce is never recognized. historyMu must be held.
func (d *daemon) sealClaimedLocked(nonce string) bool {
	return nonce != "" && slices.Contains(d.sealedPreCompacts, nonce)
}

// claimSealLocked records nonce as a PreCompact this daemon is sealing, keeping the newest
// maxSealedPreCompacts, and reports whether it recorded one. historyMu must be held.
func (d *daemon) claimSealLocked(nonce string) bool {
	if nonce == "" {
		return false
	}
	d.sealedPreCompacts = append(d.sealedPreCompacts, nonce)
	if n := len(d.sealedPreCompacts) - maxSealedPreCompacts; n > 0 {
		d.sealedPreCompacts = slices.Delete(d.sealedPreCompacts, 0, n)
	}
	return true
}

// releaseSealLocked drops a claim whose seal did not succeed. historyMu must be held.
func (d *daemon) releaseSealLocked(nonce string) {
	if i := slices.Index(d.sealedPreCompacts, nonce); i >= 0 {
		d.sealedPreCompacts = slices.Delete(d.sealedPreCompacts, i, i+1)
	}
}

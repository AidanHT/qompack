package daemon

import "slices"

// The record handleCheckpoint keeps of each PreCompact it has sealed, so a hook's spooled copy of one
// is not sealed again (V6 close-out audit 2, finding 4; handleCheckpoint says why).

// maxSealedPreCompacts bounds daemon.sealedPreCompacts. A spooled copy of a PreCompact reaches a
// drain within that compaction's aftermath (the client-spool watcher this route kicks, the idle
// drain, Stop's drain), long before eight more compactions have been sealed; one older than that is
// sealed again, as every copy was before the record existed.
const maxSealedPreCompacts = 8

// sealedLocked reports whether a seal of the PreCompact delivered under nonce has succeeded in this
// daemon (noteSealedLocked). A request with no nonce is never recognized. historyMu must be held.
func (d *daemon) sealedLocked(nonce string) bool {
	return nonce != "" && slices.Contains(d.sealedPreCompacts, nonce)
}

// noteSealedLocked records that a seal of the PreCompact delivered under nonce has succeeded, keeping
// the newest maxSealedPreCompacts. A seal that is still running or that failed is never recorded, so
// a copy that reaches a drain meanwhile is sealed. historyMu must be held.
func (d *daemon) noteSealedLocked(nonce string) {
	if nonce == "" || slices.Contains(d.sealedPreCompacts, nonce) {
		return
	}
	d.sealedPreCompacts = append(d.sealedPreCompacts, nonce)
	if n := len(d.sealedPreCompacts) - maxSealedPreCompacts; n > 0 {
		d.sealedPreCompacts = slices.Delete(d.sealedPreCompacts, 0, n)
	}
}

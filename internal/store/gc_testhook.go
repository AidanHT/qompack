package store

import "sync/atomic"

// gcAfterHarvest, when set, runs in harvestHashes after every retention source has been harvested and
// before the delivery-segment authority is rechecked: the window in which a rotation that moves the
// authority must make the pass halt rather than collect against the sources it read. It is nil in
// production; only SetGCAfterHarvestHookForTest sets it.
var gcAfterHarvest atomic.Pointer[func()]

// SetGCAfterHarvestHookForTest makes every GC pass call f after harvesting its retention sources and
// before rechecking the delivery-segment authority, and returns the function that removes it. It exists
// so internal/daemon's live-rotation GC test can move the authority inside that window
// deterministically (V6 close-out C1.10, review finding 6); nothing else may call it.
func SetGCAfterHarvestHookForTest(f func()) (restore func()) {
	prev := gcAfterHarvest.Swap(&f)
	return func() { gcAfterHarvest.Store(prev) }
}

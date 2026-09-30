package checkpoint

// This file exports one package-internal seam to the external checkpoint_test package, which is
// where every test of Finalize lives. It is a _test.go file, so nothing here ships in a binary.

import (
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
)

// SetBarriersForTest replaces the durability barriers w seals checkpoints through. A test uses it to
// count the barriers a seal issues and to cut the seal at one of them, the way a power loss would
// (finalize_durability_test.go). The zero Barriers restores the real ones.
func SetBarriersForTest(w *FileWriter, b paths.Barriers) { w.barriers = b }

// PromptCacheLenForTest is how many prompt texts d's intent refresh holds in memory
// (intent_evolution_test.go pins that it is bounded by what the draft keeps).
func PromptCacheLenForTest(d *Draft) int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.promptText)
}

// HandOffForTest stashes d's prompt-text cache the way afterSeal does before opening a successor.
func HandOffForTest(w *FileWriter, d *Draft) { w.handOff(d) }

// HandoffPendingForTest reports whether a stashed cache for s is still waiting to be consumed.
func HandoffPendingForTest(w *FileWriter, s core.SessionID) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	_, ok := w.handoff[s]
	return ok
}

// DraftScansForTest reports how many times w has scanned state/ for the claim floor
// (persistedClaimFloor): once per writer, however many drafts it begins.
func DraftScansForTest(w *FileWriter) int {
	w.claimFloorMu.Lock()
	defer w.claimFloorMu.Unlock()
	return w.draftScans
}

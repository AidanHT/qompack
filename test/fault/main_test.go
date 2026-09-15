package fault

import (
	"os"
	"testing"
)

// TestMain prunes the artifact directory BEFORE the first case, writes this run's manifest after the
// last one, and takes away the assembled bundle.
//
// The prune moved here from the first writeRecord, and the reason is a hole round 1 left: the prune
// and the manifest were both lazy, so a collecting run that fatalled before any record was written
// neither cleared the previous run's records nor rewrote INDEX.json — and the committed directory
// would then present a complete, stale evidence table that nothing distinguished from a fresh one.
// Priming it here makes the directory empty from the moment the run starts, whatever happens next.
//
// The bundle directory outlives the test that triggered its assembly (it is os.MkdirTemp, not
// t.TempDir), so this is the only place that can remove it.
func TestMain(m *testing.M) {
	primeArtifactDir()
	code := m.Run()
	writeArtifactIndex()
	removeBundle()
	os.Exit(code)
}

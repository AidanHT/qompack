package platform

import (
	"os"
	"testing"
)

// TestMain writes the artifact manifest and takes away the assembled bundle after the last case.
//
// Both belong here and nowhere else. The bundle directory outlives the test that triggered the
// assembly (it is os.MkdirTemp, not t.TempDir — see assembledBundle), so this is the only place
// that can remove it; and the manifest names which records this run actually produced, which is
// only knowable once every case has had its chance to write one.
func TestMain(m *testing.M) {
	code := m.Run()
	// The manifest first, because it is evidence about the run that just finished; removing the
	// bundle is only housekeeping.
	writeArtifactIndex()
	removeBundle()
	os.Exit(code)
}

package release

import (
	"os"
	"testing"

	"github.com/qompack/qompack/internal/paths/pathstest"
)

// TestMain writes the artifact manifest and takes away the assembled bundle after the last case.
//
// Both belong here and nowhere else. The bundle directory outlives the test that triggered the
// assembly (it is os.MkdirTemp, not t.TempDir — see assembledBundle), so this is the only place
// that can remove it; and the manifest names which records this run actually produced, which is
// only knowable once every case has had its chance to write one.
//
// Every case runs with the user's home isolated (pathstest.Main): the binaries and daemons these
// cases start inherit HOME and USERPROFILE, and no test may read or write the real ~/.qompack or
// ~/.claude (test/guards' TestGuard_EveryHomeReachingTestPackageIsolatesHome).
func TestMain(m *testing.M) { os.Exit(pathstest.Main(m, writeArtifactIndex, removeBundle)) }

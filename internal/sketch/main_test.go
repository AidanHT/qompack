package sketch

import (
	"os"
	"testing"

	"github.com/qompack/qompack/internal/paths/pathstest"
)

// TestMain runs this package's tests with the user's home isolated (pathstest.Main). Its test binary
// links a package that resolves the home, and no test may read or write the real ~/.qompack or
// ~/.claude (test/guards' TestGuard_EveryHomeReachingTestPackageIsolatesHome).
func TestMain(m *testing.M) { os.Exit(pathstest.Main(m)) }

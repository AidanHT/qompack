package cli

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/paths/pathstest"
)

// TestMain runs this package's tests with the user's home isolated (pathstest.Main). Its test binary
// links a package that resolves the home, and no test may read or write the real ~/.qompack or
// ~/.claude (test/guards' TestGuard_EveryHomeReachingTestPackageIsolatesHome).
//
// It also fails the run when any test spooled a hook delivery into the checkout's own store
// (checkoutSpoolGuard): an in-process hook whose Env pins no QOMPACK_PROJECT_ROOT takes its first
// project root from the process cwd, which under go test is inside the checkout, and a degraded
// delivery is never re-rooted to the payload's cwd.
func TestMain(m *testing.M) {
	guard, err := newCheckoutSpoolGuard()
	if err != nil {
		fmt.Fprintf(os.Stderr, "checkout spool guard: %v; no test was run\n", err)
		os.Exit(1)
	}
	code := pathstest.Main(m)
	if err := guard.check(); err != nil {
		fmt.Fprintf(os.Stderr, "checkout spool guard: %v\n", err)
		if code == 0 {
			code = 1
		}
	}
	os.Exit(code)
}

// checkoutSpoolGuard watches the one file an in-process hook of this test binary appends to when it
// lands in the checkout: <root>/.qompack/spool/client-<this pid>.ndjson, where root is what the
// hook client's first resolution gives with no environment (resolveProjectRoot with an empty
// Getenv and no Event). It names this process's file only, so a real Qompack session working in
// the same checkout, whose hooks are other processes, can never make it fail.
type checkoutSpoolGuard struct {
	path   string
	before int64 // the file's size before the run; -1 when it did not exist
}

func newCheckoutSpoolGuard() (checkoutSpoolGuard, error) {
	root := resolveProjectRoot(Env{Getenv: func(string) string { return "" }}, nil)
	if root == "" {
		return checkoutSpoolGuard{}, errors.New("the process cwd resolves to no project root")
	}
	g := checkoutSpoolGuard{
		path: filepath.Join(paths.Of(root).Spool, fmt.Sprintf("client-%d.ndjson", os.Getpid())),
	}
	size, err := fileSizeOrAbsent(g.path)
	g.before = size
	return g, err
}

// check reports a spool file of this process that appeared or grew during the run.
func (g checkoutSpoolGuard) check() error {
	after, err := fileSizeOrAbsent(g.path)
	if err != nil {
		return err
	}
	if after == g.before {
		return nil
	}
	return fmt.Errorf("a test spooled hook deliveries into the checkout's store: %s went from %d to "+
		"%d bytes (-1 is absent). Pin QOMPACK_PROJECT_ROOT to the test's temp project in its Env, "+
		"then delete the file", g.path, g.before, after)
}

func fileSizeOrAbsent(p string) (int64, error) {
	fi, err := os.Stat(p)
	if errors.Is(err, fs.ErrNotExist) {
		return -1, nil
	}
	if err != nil {
		return 0, err
	}
	return fi.Size(), nil
}

package cli

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/paths/pathstest"
)

// TestMain runs this package's tests with the user's home isolated (pathstest.Main). Its test binary
// links a package that resolves the home, and no test may read or write the real ~/.qompack or
// ~/.claude (test/guards' TestGuard_EveryHomeReachingTestPackageIsolatesHome).
//
// It also fails the run when any test wrote into the checkout's own store (checkoutSpoolGuard): an
// in-process hook whose Env pins no QOMPACK_PROJECT_ROOT takes its first project root from the
// process cwd, which under go test is inside the checkout, and a degraded delivery is never
// re-rooted to the payload's cwd.
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

// checkoutSpoolGuard watches the checkout's store, <root>/.qompack, where root is what the hook
// client's first resolution gives with no environment (resolveProjectRoot with an empty Getenv and
// no Event). When the store did not exist before the run, any store the run leaves behind fails
// it, whatever wrote it: a spooled delivery, an externalized blob, a log, a daemon spawned at the
// checkout root. When it did exist, a real Qompack session may be working in the same checkout, so
// the guard watches only the one file an in-process hook of this test binary appends to there,
// <root>/.qompack/spool/client-<this pid>.ndjson; that session's hooks are other processes and can
// never make it fail.
type checkoutSpoolGuard struct {
	dot        string
	dotExisted bool // whether <root>/.qompack existed before the run
	path       string
	before     int64 // the file's size before the run; -1 when it did not exist
}

func newCheckoutSpoolGuard() (checkoutSpoolGuard, error) {
	root := resolveProjectRoot(Env{Getenv: func(string) string { return "" }}, nil)
	if root == "" {
		return checkoutSpoolGuard{}, errors.New("the process cwd resolves to no project root")
	}
	return newCheckoutSpoolGuardAt(root)
}

// newCheckoutSpoolGuardAt is newCheckoutSpoolGuard for a given root.
func newCheckoutSpoolGuardAt(root string) (checkoutSpoolGuard, error) {
	layout := paths.Of(root)
	g := checkoutSpoolGuard{
		dot:  layout.Dot,
		path: filepath.Join(layout.Spool, fmt.Sprintf("client-%d.ndjson", os.Getpid())),
	}
	if _, err := os.Lstat(g.dot); err == nil {
		g.dotExisted = true
	} else if !errors.Is(err, fs.ErrNotExist) {
		return g, err
	}
	size, err := fileSizeOrAbsent(g.path)
	g.before = size
	return g, err
}

// check reports a store the run created, or a spool file of this process that appeared or grew
// during the run.
func (g checkoutSpoolGuard) check() error {
	if !g.dotExisted {
		return g.checkNoStore()
	}
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

// checkNoStore reports a store the run created where there was none, naming every file in it.
func (g checkoutSpoolGuard) checkNoStore() error {
	if _, err := os.Lstat(g.dot); errors.Is(err, fs.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	var inside []string
	walkErr := filepath.WalkDir(g.dot, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			rel, relErr := filepath.Rel(g.dot, p)
			if relErr != nil {
				return relErr
			}
			inside = append(inside, filepath.ToSlash(rel))
		}
		return nil
	})
	if walkErr != nil {
		inside = append(inside, fmt.Sprintf("(listing stopped: %v)", walkErr))
	}
	return fmt.Errorf("a test created the checkout's store %s, which did not exist before the run; "+
		"it holds %s. Pin QOMPACK_PROJECT_ROOT to the test's temp project in its Env, then delete "+
		"the directory", g.dot, strings.Join(inside, ", "))
}

package cli

import (
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
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
// the guard watches only the files an in-process hook of this test binary can write there: this
// process's client spools, <root>/.qompack/spool/client-<this pid>-<writer id>.ndjson, one per
// spool writer, and client-<this pid>.ndjson, the name a 0.3.0 writer of this pid used. That
// session's hooks are other processes, and their spools carry their own pids, so they can never
// make it fail.
type checkoutSpoolGuard struct {
	dot        string
	dotExisted bool // whether <root>/.qompack existed before the run
	spool      string
	pid        int
	before     map[string]int64 // this process's client spools before the run, by name, with their sizes
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
	g := checkoutSpoolGuard{dot: layout.Dot, spool: layout.Spool, pid: os.Getpid()}
	if _, err := os.Lstat(g.dot); err == nil {
		g.dotExisted = true
	} else if !errors.Is(err, fs.ErrNotExist) {
		return g, err
	}
	before, err := g.ownSpools()
	g.before = before
	return g, err
}

// check reports a store the run created, or a client spool of this process that appeared, changed
// size or went away during the run.
func (g checkoutSpoolGuard) check() error {
	if !g.dotExisted {
		return g.checkNoStore()
	}
	after, err := g.ownSpools()
	if err != nil {
		return err
	}
	names := map[string]bool{}
	for name := range g.before {
		names[name] = true
	}
	for name := range after {
		names[name] = true
	}
	for _, name := range slices.Sorted(maps.Keys(names)) {
		was, is := sizeOrAbsent(g.before, name), sizeOrAbsent(after, name)
		if was == is {
			continue
		}
		return fmt.Errorf("a test spooled hook deliveries into the checkout's store: %s went from %d to "+
			"%d bytes (-1 is absent). Pin QOMPACK_PROJECT_ROOT to the test's temp project in its Env, "+
			"then delete the file", filepath.Join(g.spool, name), was, is)
	}
	return nil
}

// ownSpools lists this process's client spools in the guarded spool directory, by name, with their
// sizes. A directory that does not exist holds none.
func (g checkoutSpoolGuard) ownSpools() (map[string]int64, error) {
	entries, err := os.ReadDir(g.spool)
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]int64{}, nil
	}
	if err != nil {
		return nil, err
	}
	legacy := fmt.Sprintf("client-%d.ndjson", g.pid)
	perWriter := fmt.Sprintf("client-%d-", g.pid)
	out := map[string]int64{}
	for _, e := range entries {
		name := e.Name()
		if name != legacy && (!strings.HasPrefix(name, perWriter) || !strings.HasSuffix(name, ".ndjson")) {
			continue
		}
		// A stat of the file itself, not the listing's entry: on Windows the directory record's size
		// can lag behind a file a writer still holds open.
		fi, err := os.Stat(filepath.Join(g.spool, name))
		if errors.Is(err, fs.ErrNotExist) {
			continue // gone between the listing and its stat: absent
		}
		if err != nil {
			return nil, err
		}
		out[name] = fi.Size()
	}
	return out, nil
}

// sizeOrAbsent is name's size in sizes, or -1 when sizes has no such file.
func sizeOrAbsent(sizes map[string]int64, name string) int64 {
	if size, ok := sizes[name]; ok {
		return size
	}
	return -1
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

// Package e2e drives the real qompack binary the way Claude Code drives it: a built executable, a
// real temp project on disk, a JSON payload on stdin, and assertions on the exit code, on stdout,
// and on what the process left behind in .qompack/.
//
// It is the other half of 00-ARCHITECTURE.md §6.2's "real binary or in-proc" requirement. The
// in-process half lives in internal/testutil, is fast enough for every unit test, and shares this
// package's payloads; this package exists because in-process dispatch cannot prove the things that
// only a process can have — an exit code the host actually observes, a stdout that is really a
// pipe, and a cold start with no state carried over from a previous test.
//
// e2e is a composition root (§3.2): it imports the whole tree, and nothing may import it.
package e2e

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
)

// buildOnce guards the single `go build` this package performs. §17 requires the binary to be
// built once per test binary, not once per test: a Go build is by far the most expensive thing
// here, and rebuilding it per test would make the e2e suite the slowest part of CI for no
// additional coverage.
var (
	buildOnce sync.Once
	builtBin  string
	buildDir  string
	buildErr  error
)

// Build compiles ./cmd/qompack into a temporary directory and returns the executable's path. The
// first caller pays for the build; every later caller gets the same path back.
func Build(t *testing.T) string {
	t.Helper()
	buildOnce.Do(doBuild)
	if buildErr != nil {
		t.Fatalf("e2e: building ./cmd/qompack: %v", buildErr)
	}
	return builtBin
}

// doBuild is Build's body, factored out so sync.Once can take it as a bare func().
//
// The output directory is created with os.MkdirTemp rather than t.TempDir because it has to
// outlive the test that triggered the build; removeBuild, called from TestMain, is what takes it
// away again.
func doBuild() {
	root, err := moduleRoot()
	if err != nil {
		buildErr = err
		return
	}

	buildDir, buildErr = os.MkdirTemp("", "qompack-e2e-")
	if buildErr != nil {
		return
	}

	out := filepath.Join(buildDir, "qompack")
	if runtime.GOOS == "windows" {
		out += ".exe"
	}

	cmd := exec.CommandContext(context.Background(), "go", "build", "-o", out, "./cmd/qompack")
	cmd.Dir = root
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		buildErr = fmt.Errorf("go build -o %s ./cmd/qompack (in %s): %w\n%s", out, root, err, stderr.String())
		return
	}
	builtBin = out
}

// removeBuild deletes the directory doBuild created. TestMain calls it after the last test.
func removeBuild() {
	if buildDir != "" {
		_ = os.RemoveAll(buildDir)
	}
}

// Run executes bin with args, feeding it stdin, and returns its stdout, stderr and exit code.
//
// env entries are added on top of the process environment, so a caller overrides only what it
// names; the parent's own QOMPACK_PROJECT_ROOT, HOME and USERPROFILE — which testutil.NewProject
// points into t.TempDir() — are inherited, which is what keeps a spawned binary confined to the
// same temp tree as the test that spawned it.
//
// The child runs in the build directory, deliberately: a neutral directory that is not the
// repository, so a test that forgets to name a project root cannot accidentally resolve the
// checkout itself as one.
//
// A failure to START the process fails the test. A non-zero exit is returned, never asserted
// here: the exit code is the thing e2e tests exist to check.
func Run(t *testing.T, bin string, args []string, stdin []byte, env map[string]string) (stdout, stderr []byte, code int) {
	t.Helper()

	cmd := exec.CommandContext(context.Background(), bin, args...)
	cmd.Dir = filepath.Dir(bin)
	cmd.Stdin = bytes.NewReader(stdin)
	cmd.Env = os.Environ()
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}

	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf

	err := cmd.Run()
	var exitErr *exec.ExitError
	switch {
	case err == nil:
		code = 0
	case errors.As(err, &exitErr):
		code = exitErr.ExitCode()
	default:
		t.Fatalf("e2e: could not run %s %v: %v\nstderr:\n%s", bin, args, err, errBuf.String())
	}
	return outBuf.Bytes(), errBuf.Bytes(), code
}

// moduleRoot returns the repository root: the nearest ancestor of the working directory holding a
// go.mod. `go build ./cmd/qompack` is only meaningful from there, and a test's working directory
// is always its own package directory.
func moduleRoot() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for d := wd; ; {
		if fi, statErr := os.Stat(filepath.Join(d, "go.mod")); statErr == nil && fi.Mode().IsRegular() {
			return d, nil
		}
		parent := filepath.Dir(d)
		if parent == d {
			return "", fmt.Errorf("no go.mod found in %s or any parent directory", wd)
		}
		d = parent
	}
}

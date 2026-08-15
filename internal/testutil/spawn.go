// spawn.go is the ONE file in internal/testutil permitted to import os/exec, and the only place
// in the package where a subprocess is created.
//
// 00-ARCHITECTURE.md §6.2 requires every hook to be exercisable two ways — "real binary or
// in-proc" — and the real-binary half necessarily spawns a process. §8's security job otherwise
// permits os/exec only in internal/daemon and internal/cli; the implementation spec §17 extends
// that allowlist with internal/testutil specifically so this import can live here rather than
// forcing the spawn into test/e2e, which would contradict §6.2's requirement that
// (*Project).RunHook itself offer both modes.
//
// Confining it to one file is what makes the extension safe and auditable: testutil is a
// composition root that nothing outside a _test.go file imports, so devtool lint's bindeps
// sub-check can prove os/exec never reaches cmd/qompack through it.

package testutil

import (
	"bytes"
	"context"
	"errors"
	"os/exec" // §6.2: the real-binary hook mode. See this file's header comment.
	"testing"
)

// spawn runs bin with args, feeding it stdin, and returns its stdout, stderr and exit code.
//
// env entries are "K=V" strings and replace the child's whole environment; a nil env inherits the
// parent's. A failure to START the process (a missing binary, a directory that does not exist)
// fails the test outright; a non-zero EXIT is returned as a code, because asserting on exit codes
// is exactly what the callers of this function exist to do.
func spawn(t *testing.T, dir, bin string, args []string, stdin []byte, env []string) (stdout, stderr []byte, code int) {
	t.Helper()

	cmd := exec.CommandContext(context.Background(), bin, args...)
	cmd.Dir = dir
	cmd.Stdin = bytes.NewReader(stdin)
	cmd.Env = env

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
		t.Fatalf("testutil: could not run %s %v in %s: %v\nstderr:\n%s", bin, args, dir, err, errBuf.String())
	}
	return outBuf.Bytes(), errBuf.Bytes(), code
}

// gitInit runs `git init --quiet` in dir.
//
// A Project becomes a git working tree only when WithGit is passed, so that paths.Resolve's .git
// walk (00-ARCHITECTURE.md §3.3 step 2) is exercised both ways: against a project that has one and
// against a project that does not. It shells out to the real git rather than fabricating a .git
// directory, because the point of the option is to produce the working tree a real host would.
func gitInit(t *testing.T, dir string) {
	t.Helper()
	_, stderr, code := spawn(t, dir, "git", []string{"init", "--quiet"}, nil, nil)
	if code != 0 {
		t.Fatalf("testutil: git init in %s exited %d:\n%s", dir, code, stderr)
	}
}

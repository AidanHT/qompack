package main

import (
	"fmt"
	"path/filepath"
)

// taskReplay runs the replay-gate driver, `go run ./test/replay`.
//
// EVERY argument after `replay` is forwarded verbatim and the child's exit code is propagated
// unchanged, so the flag surface documented in SP-02's implementation spec is the flag surface CI
// uses and there is no second place to keep in sync. That matters because the driver's exit codes
// are load-bearing — 1 is a gate failure, 2 is bad input, 3 is either of the driver's self-imposed
// replay limits (--max-cpu's cost budget or --max-wall's liveness ceiling, which print different
// sentences), 5 is phase checks disabled under --ci — and collapsing them here would tell a CI log
// nothing.
func taskReplay(args []string) error {
	dir := filepath.Join(root, "test", "replay")
	if !dirHasGoFiles(dir) {
		return fmt.Errorf("replay: no driver at %s", dir)
	}
	runArgs := append([]string{"run", "./test/replay"}, args...)
	return goInherit(runArgs...)
}

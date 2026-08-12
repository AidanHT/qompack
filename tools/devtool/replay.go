package main

import (
	"fmt"
	"path/filepath"
)

// taskReplay runs the replay-gate driver, `go run ./test/replay`, forwarding args, if that
// driver exists. SP-02 owns it; before SP-02 lands this degrades to a zero-exit no-op, exactly as
// the implementation spec's task table requires.
func taskReplay(args []string) error {
	dir := filepath.Join(root, "test", "replay")
	if !dirHasGoFiles(dir) {
		fmt.Println("replay: driver not present (owned by SP-02)")
		return nil
	}
	runArgs := append([]string{"run", "./test/replay"}, args...)
	return goInherit(runArgs...)
}

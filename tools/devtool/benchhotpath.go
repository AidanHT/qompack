package main

import (
	"fmt"
	"path/filepath"
)

// taskBenchHotpath runs the real-process-spawn hot-path harness, `go run ./test/bench/hotpath`,
// forwarding args. SP-05 (task 7) has landed test/bench/hotpath, so dirHasGoFiles is now
// permanently true in this tree; the fallback below is kept only as the documented degrade path
// the implementation spec's task table requires for a build that predates it (e.g. a bisect).
func taskBenchHotpath(args []string) error {
	dir := filepath.Join(root, "test", "bench", "hotpath")
	if !dirHasGoFiles(dir) {
		fmt.Println("bench-hotpath: harness not present (owned by SP-05)")
		return nil
	}
	runArgs := append([]string{"run", "./test/bench/hotpath"}, args...)
	return goInherit(runArgs...)
}

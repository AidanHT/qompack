package main

import (
	"fmt"
	"path/filepath"
)

// taskBenchHotpath runs the real-process-spawn hot-path harness, `go run ./test/bench/hotpath`,
// forwarding args, if that harness exists. SP-05 owns it; before SP-05 lands this degrades to a
// zero-exit no-op, exactly as the implementation spec's task table requires.
func taskBenchHotpath(args []string) error {
	dir := filepath.Join(root, "test", "bench", "hotpath")
	if !dirHasGoFiles(dir) {
		fmt.Println("bench-hotpath: harness not present (owned by SP-05)")
		return nil
	}
	runArgs := append([]string{"run", "./test/bench/hotpath"}, args...)
	return goInherit(runArgs...)
}

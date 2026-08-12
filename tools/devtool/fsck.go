package main

import (
	"fmt"
	"path/filepath"
)

// taskFsck runs `go run ./cmd/qompack fsck`, degrading to a zero-exit no-op before cmd/qompack
// exists (the main session's Phase C deliverable within this same subplan).
func taskFsck(args []string) error {
	dir := filepath.Join(root, "cmd", "qompack")
	if !dirHasGoFiles(dir) {
		fmt.Println("fsck: cmd/qompack not present yet (owned by SP-01 main session)")
		return nil
	}
	runArgs := append([]string{"run", "./cmd/qompack", "fsck"}, args...)
	return goInherit(runArgs...)
}

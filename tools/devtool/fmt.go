package main

import (
	"fmt"
	"strings"
)

// taskFmt runs the pinned gofumpt with -w, formatting every file in place. Per the implementation
// spec's task table this never fails on its own account (formatting is mutating, not a gate) — any
// underlying gofumpt error is still surfaced on stderr via the inherited streams.
func taskFmt(args []string) error {
	_ = pinnedRunInherit(gofumptPkg, "-l", "-w", ".")
	return nil
}

// taskFmtCheck runs gofumpt -l (list only, no write) and fails if it lists any file, printing the
// offending files exactly as gofumpt reports them.
func taskFmtCheck(args []string) error {
	stdout, stderr, err := pinnedCapture(gofumptPkg, "-l", ".")
	if len(stderr) > 0 {
		fmt.Print(string(stderr))
	}
	if err != nil {
		return fmt.Errorf("gofumpt -l: %w", err)
	}
	trimmed := strings.TrimSpace(string(stdout))
	if trimmed == "" {
		return nil
	}
	fmt.Println(trimmed)
	return fmt.Errorf("fmt-check: %d file(s) not gofumpt-formatted", len(strings.Split(trimmed, "\n")))
}

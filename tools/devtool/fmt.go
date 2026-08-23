package main

import (
	"fmt"
	"os"
	"strings"
)

// fmtSkipDirs are the top-level directories gofumpt is never pointed at.
//
// .claude/worktrees holds a full checkout per agent (.gitignore), and `gofumpt ... .` walks it
// like any other directory. That breaks both tasks below, in opposite directions. fmt-check
// reports every stale copy of a file as its own offender, so a real violation arrives buried in
// phantom ones — which is how the gofumpt violation in stubskips_test.go survived from 0b1247b
// until CI's own fmt-check was replayed by hand. And fmt, which runs with -w, would REWRITE
// another branch's working tree: a formatter that edits a sibling worktree is one that can lose
// somebody's uncommitted work.
//
// .git is skipped for the ordinary reason. Both are matched by name at the top level only,
// because that is where they live and a package legitimately named "claude" deeper in the tree
// should still be formatted.
var fmtSkipDirs = map[string]bool{".git": true, ".claude": true}

// fmtTargets lists what to hand gofumpt: every top-level .go file and every top-level directory
// that is not skipped.
//
// It is enumerated rather than hardcoded so a new top-level package is covered the day it lands —
// a formatting gate that silently stops covering a directory is the same failure as one that
// reports files nobody can act on.
func fmtTargets(root string) ([]string, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("read the module root: %w", err)
	}
	var out []string
	for _, e := range entries {
		name := e.Name()
		switch {
		case e.IsDir():
			if fmtSkipDirs[name] {
				continue
			}
			out = append(out, "./"+name)
		case strings.HasSuffix(name, ".go"):
			out = append(out, "./"+name)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no Go sources under the module root")
	}
	return out, nil
}

// taskFmt runs the pinned gofumpt with -w, formatting every file in place. Per the implementation
// spec's task table this never fails on its own account (formatting is mutating, not a gate) — any
// underlying gofumpt error is still surfaced on stderr via the inherited streams.
func taskFmt(args []string) error {
	targets, err := fmtTargets(".")
	if err != nil {
		return err
	}
	_ = pinnedRunInherit(gofumptPkg, append([]string{"-l", "-w"}, targets...)...)
	return nil
}

// taskFmtCheck runs gofumpt -l (list only, no write) and fails if it lists any file, printing the
// offending files exactly as gofumpt reports them.
func taskFmtCheck(args []string) error {
	targets, err := fmtTargets(".")
	if err != nil {
		return err
	}
	stdout, stderr, err := pinnedCapture(gofumptPkg, append([]string{"-l"}, targets...)...)
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

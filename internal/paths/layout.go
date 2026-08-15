package paths

import (
	"fmt"
	"os"
	"path/filepath"
)

// dotDir is the name of the runtime store directory at the root of every project (§3.3).
const dotDir = ".qompack"

// gitignoreContents is the exact, complete content EnsureLayout writes to .qompack/.gitignore so
// the store self-ignores even in a project whose own .gitignore is never touched (§3.3).
const gitignoreContents = "*\n"

// Layout names every directory in one project's .qompack/ runtime tree (§3.3). Of builds a
// Layout from a project root without touching the filesystem; EnsureLayout creates the
// directories the Layout names.
type Layout struct {
	Root, Dot                                      string // project root, <root>/.qompack
	Objects, Index, Sketches, DAG                  string
	Grammar, Checkpoints, Pins, Eval               string
	Records, State, Run, Spool, Logs, Metrics, Tmp string
}

// Of returns the Layout for root, a project root as returned by Resolve. It is a pure function
// over strings: call EnsureLayout to actually create the directories on disk.
func Of(root string) Layout {
	dot := filepath.Join(root, dotDir)
	return Layout{
		Root:        root,
		Dot:         dot,
		Objects:     filepath.Join(dot, "objects"),
		Index:       filepath.Join(dot, "index"),
		Sketches:    filepath.Join(dot, "sketches"),
		DAG:         filepath.Join(dot, "dag"),
		Grammar:     filepath.Join(dot, "grammar"),
		Checkpoints: filepath.Join(dot, "checkpoints"),
		Pins:        filepath.Join(dot, "pins"),
		Eval:        filepath.Join(dot, "eval"),
		Records:     filepath.Join(dot, "records"),
		State:       filepath.Join(dot, "state"),
		Run:         filepath.Join(dot, "run"),
		Spool:       filepath.Join(dot, "spool"),
		Logs:        filepath.Join(dot, "logs"),
		Metrics:     filepath.Join(dot, "metrics"),
		Tmp:         filepath.Join(dot, "tmp"),
	}
}

// EnsureLayout creates every directory l names — objects, index, sketches, dag, grammar,
// checkpoints, pins, eval/replay, eval/opt, records, state, run, spool, logs, metrics and tmp —
// with 0o700 permissions (eval/replay and eval/opt bring l.Eval itself into existence as their
// parent). It then writes <root>/.qompack/.gitignore containing exactly "*\n" through
// WriteAtomic, unless that file already exists, so a repeated call is idempotent.
func EnsureLayout(l Layout) error {
	dirs := []string{
		l.Objects, l.Index, l.Sketches, l.DAG, l.Grammar, l.Checkpoints, l.Pins,
		filepath.Join(l.Eval, "replay"), filepath.Join(l.Eval, "opt"),
		l.Records, l.State, l.Run, l.Spool, l.Logs, l.Metrics, l.Tmp,
	}
	for _, d := range dirs {
		if err := os.MkdirAll(Long(d), 0o700); err != nil {
			return fmt.Errorf("paths: EnsureLayout: mkdir %s: %w", d, err)
		}
	}

	gitignore := filepath.Join(l.Dot, ".gitignore")
	if _, err := os.Stat(Long(gitignore)); err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("paths: EnsureLayout: stat %s: %w", gitignore, err)
		}
		if err := WriteAtomic(gitignore, []byte(gitignoreContents), 0o600); err != nil {
			return fmt.Errorf("paths: EnsureLayout: write %s: %w", gitignore, err)
		}
	}
	return nil
}

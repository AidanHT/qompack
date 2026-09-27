package paths

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
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
	// Migrate holds the legacy-import cursor, identity mapping, parity report, writer-handoff
	// record and rollback-drill record (SP-20 M1-04). It is separate from State because its
	// contents outlive a single run and are read by the migration drill, not by the daemon.
	Migrate string
	// Backup holds one directory per consistent backup taken before a cutover or a rollback
	// rehearsal, each with its own verifiable manifest (SP-20 M1-04, ADR 0013 "consistent
	// backup and outer migration authority remain required").
	Backup string
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
		Migrate:     filepath.Join(dot, "migrate"),
		Backup:      filepath.Join(dot, "backup"),
	}
}

// EnsureLayout creates every directory l names — objects, index, sketches, dag, grammar,
// checkpoints, pins, eval/replay, eval/opt, records, state, run, spool, logs, metrics, tmp,
// migrate and backup —
// with 0o700 permissions (eval/replay and eval/opt bring l.Eval itself into existence as their
// parent). It then writes <root>/.qompack/.gitignore containing exactly "*\n" through
// WriteAtomic, unless that file already exists, so a repeated call is idempotent.
//
// Every directory it creates is made durable before it returns: the parent of each one is synced,
// deepest first, up to the project root when .qompack itself is new. Every durability promise the
// store makes is a file inside one of these directories, and on POSIX a file's own sync — or a sync
// of the directory holding it — does not make that directory's own name durable. A call that creates
// nothing syncs nothing, so this costs a few directory syncs once per project (and once more when a
// newer build adds a directory), never per call.
func EnsureLayout(l Layout) error { return Barriers{}.EnsureLayout(l) }

// EnsureLayout is the package function of the same name, issuing its directory syncs through x.
func (x Barriers) EnsureLayout(l Layout) error {
	dirs := []string{
		l.Objects, l.Index, l.Sketches, l.DAG, l.Grammar, l.Checkpoints, l.Pins,
		filepath.Join(l.Eval, "replay"), filepath.Join(l.Eval, "opt"),
		l.Records, l.State, l.Run, l.Spool, l.Logs, l.Metrics, l.Tmp,
		l.Migrate, l.Backup,
	}
	missing := func(p string) bool {
		_, err := os.Lstat(Long(p))
		return errors.Is(err, fs.ErrNotExist)
	}
	// parents collects the directories whose entries this call adds: the parent of every directory it
	// creates, including the ones MkdirAll creates on the way (.qompack itself, eval/).
	parents := map[string]bool{}
	for _, d := range []string{l.Dot, l.Eval} {
		if missing(d) {
			parents[filepath.Dir(d)] = true
		}
	}
	for _, d := range dirs {
		if missing(d) {
			parents[filepath.Dir(d)] = true
		}
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

	// Deepest first, so a directory's entry is made durable only after the entries inside it.
	order := make([]string, 0, len(parents))
	for p := range parents {
		order = append(order, p)
	}
	sort.Slice(order, func(i, j int) bool {
		if len(order[i]) != len(order[j]) {
			return len(order[i]) > len(order[j])
		}
		return order[i] < order[j]
	})
	for _, p := range order {
		if err := x.syncDir(p); err != nil {
			return fmt.Errorf("paths: EnsureLayout: sync %s: %w", p, err)
		}
	}
	return nil
}

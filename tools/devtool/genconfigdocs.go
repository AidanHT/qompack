package main

import (
	"errors"
	"flag"
)

// taskGenConfigDocs renders docs/config-reference.md from config.Defaults() plus its JSON Schema
// metadata (implementation spec §3, "the one docs exception"); --check diffs instead of writing.
//
// internal/config is a later commit within this same subplan and does not exist yet, so this task
// cannot import it without making this commit unbuildable. It returns a clear, typed error until
// the config commit wires the real generator in here.
func taskGenConfigDocs(args []string) error {
	fs := flag.NewFlagSet("gen-config-docs", flag.ContinueOnError)
	_ = fs.Bool("check", false, "diff docs/config-reference.md against config.Defaults() instead of writing it")
	if err := fs.Parse(args); err != nil {
		return errors.Join(errUsage, err)
	}
	return errors.New("gen-config-docs: internal/config is not wired yet (landed by the config commit of this subplan)")
}

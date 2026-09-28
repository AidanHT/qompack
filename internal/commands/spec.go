package commands

import (
	"fmt"
	"io"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/pluginmanifest"
)

// Spec is one command's user-facing surface: what it is called, what it does, what it accepts.
//
// Name, Summary, ArgumentHint and Subcommand are READ FROM internal/pluginmanifest rather than
// restated here. That package generates plugin/commands/*.md, which CI diffs against the committed
// tree, so it is already the installed source of truth; restating its prose in a second table is
// how `qompack status --help` and /qompack:status come to describe the command differently.
//
// The direction is forced as well as chosen: pluginmanifest's §3.2 allow-set is empty, so it could
// not import this package even if that were the better factoring. commands is a composition root
// and may import it.
type Spec struct {
	// Name is the §7.5 name, as typed after the plugin prefix.
	Name string
	// Summary is the one-line description, identical to the installed manifest's.
	Summary string
	// ArgumentHint is the host's argument-hint string, identical to the installed manifest's.
	ArgumentHint string
	// Subcommand is the `qompack <subcommand>` route the slash command shells out to.
	Subcommand string
	// Flags are the flags this command accepts, in the order help presents them.
	Flags []FlagSpec
}

// FlagSpec is one documented flag. Name carries no leading dashes.
type FlagSpec struct {
	Name    string
	Summary string
	// Arg names the flag's value for the help line; empty means the flag is a boolean.
	Arg string
}

// jsonFlag is offered by every command. A caller scripting against qompack should never have to
// remember which of them speak JSON, so all of them do.
var jsonFlag = FlagSpec{Name: "json", Summary: "emit the stable JSON envelope instead of text"}

// flagSpecs is the per-command flag table. A command absent from the map accepts --json alone.
//
// This is the one part of a Spec that is NOT in the manifest: plugin/commands/*.md carries an
// argument hint for the host's completion UI, not a flag list, so the manifest has nowhere to put
// this and it lives here.
var flagSpecs = map[string][]FlagSpec{
	"recall": {
		{Name: "k", Summary: "return at most N results", Arg: "N"},
	},
	"pin": {
		{Name: "list", Summary: "list the pins in scope instead of adding one"},
		{Name: "remove", Summary: "remove the pin with this id", Arg: "id"},
		{Name: "source", Summary: "record the pin as user or agent authority (default user)", Arg: "who"},
		{Name: "eliminated", Summary: "record an eliminated approach instead of an invariant"},
		{Name: "target", Summary: "with --eliminated: what the approach was applied to", Arg: "text"},
		{Name: "approach", Summary: "with --eliminated: what was tried", Arg: "text"},
		{Name: "reason", Summary: "with --eliminated: why it did not work", Arg: "text"},
		{Name: "depends-on", Summary: "with --eliminated: comma-separated paths the finding rests on", Arg: "paths"},
		{Name: "scope", Summary: "with --eliminated: session or project", Arg: "scope"},
	},
	"eval": {
		{Name: "corpus", Summary: "read a live-eval run, a directory of runs, or a replay report", Arg: "path"},
	},
}

// Specs returns one Spec per §7.5 name, in the order the manifest states them.
func Specs() []Spec {
	docs := pluginmanifest.Default(core.Version).Commands
	out := make([]Spec, 0, len(docs))
	for _, d := range docs {
		flags := make([]FlagSpec, 0, len(flagSpecs[d.Name])+1)
		flags = append(flags, jsonFlag)
		flags = append(flags, flagSpecs[d.Name]...)
		out = append(out, Spec{
			Name:         d.Name,
			Summary:      d.Description,
			ArgumentHint: d.ArgumentHint,
			Subcommand:   d.Subcommand,
			Flags:        flags,
		})
	}
	return out
}

// SpecFor returns the Spec for name.
func SpecFor(name string) (Spec, bool) {
	for _, s := range Specs() {
		if s.Name == name {
			return s, true
		}
	}
	return Spec{}, false
}

// helpFlagColumn is the column the flag summaries line up in. It is a layout constant, not a
// configurable value.
const helpFlagColumn = 22

// WriteHelp renders this command's documented help.
//
// The output is deterministic and free of ANSI escapes: it is read as often by a test fixture and
// a generated markdown file as by a person at a terminal, and a colour code in the middle of a
// golden file is a diff nobody can read.
func (s Spec) WriteHelp(w io.Writer) error {
	if _, err := fmt.Fprintf(w, "qompack %s — %s\n\n", s.Subcommand, s.Summary); err != nil {
		return err
	}
	hint := s.ArgumentHint
	if hint != "" {
		hint = " " + hint
	}
	if _, err := fmt.Fprintf(w, "usage: qompack %s%s\n", s.Subcommand, hint); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "       /qompack:%s%s\n", s.Name, hint); err != nil {
		return err
	}
	if len(s.Flags) == 0 {
		return nil
	}
	if _, err := fmt.Fprint(w, "\nflags:\n"); err != nil {
		return err
	}
	for _, f := range s.Flags {
		label := "--" + f.Name
		if f.Arg != "" {
			label += " <" + f.Arg + ">"
		}
		if _, err := fmt.Fprintf(w, "  %-*s %s\n", helpFlagColumn, label, f.Summary); err != nil {
			return err
		}
	}
	return nil
}

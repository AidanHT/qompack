package commands

import (
	"context"
	"fmt"
	"io"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/contract"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/eval"
	"github.com/qompack/qompack/internal/negknow"
	"github.com/qompack/qompack/internal/obs"
	"github.com/qompack/qompack/internal/pins"
	"github.com/qompack/qompack/internal/scheduler"
	"github.com/qompack/qompack/internal/store"
)

// Command is one slash-command backend (00-ARCHITECTURE.md §5.17).
type Command interface {
	// Name is the command as the user types it after the plugin prefix: `status` for
	// `/qompack:status`.
	Name() string
	// Run executes the command, writing its rendered output to out.
	Run(ctx context.Context, args []string, out io.Writer) error
}

// Deps is the late-bound dependency set every command draws from.
//
// Every field may be nil. A command whose dependency is absent reports that plainly rather than
// panicking: during waves 1–2 most of these are genuinely not built yet, and "not available in
// this build" is a useful answer where a nil dereference is not.
type Deps struct {
	Store       store.Store
	Ledger      negknow.Ledger
	Checkpoints checkpoint.Reader
	Writer      checkpoint.Writer
	Pins        pins.Store
	Sched       scheduler.Runtime
	Eval        eval.Harness
	Metrics     obs.Registry
	Contract    contract.Monitor
	Cfg         config.Config
}

// commandNames is the §5.17 list, in the order `/qompack:help` should present them: the three a
// user reaches for daily first, then the three that explain what happened, then the harness.
var commandNames = []string{"status", "recall", "pin", "checkpoint", "why", "dropped", "eval"}

// All returns one Command per §5.17 name.
//
// The list is complete from wave 0 and its length never changes; SP-14 replaces bodies, not
// entries. That is what lets plugin/commands/*.md — which is generated from a typed source and
// diffed in CI — be written once and stay correct.
func All(d Deps) []Command {
	cmds := make([]Command, 0, len(commandNames))
	for _, name := range commandNames {
		cmds = append(cmds, stubCommand{name: name, deps: d})
	}
	return cmds
}

// Names returns the §5.17 command names. It exists so a caller can enumerate the surface without
// constructing Deps it does not have.
func Names() []string {
	out := make([]string, len(commandNames))
	copy(out, commandNames)
	return out
}

// stubCommand reports core.ErrNotImplemented for every command until SP-14 lands.
type stubCommand struct {
	name string
	deps Deps
}

func (c stubCommand) Name() string { return c.name }

func (c stubCommand) Run(_ context.Context, _ []string, _ io.Writer) error {
	return fmt.Errorf("%w: /qompack:%s (SP-14)", core.ErrNotImplemented, c.name)
}

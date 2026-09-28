package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/contract"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/eval"
	"github.com/qompack/qompack/internal/mcp"
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
	// MCP is the registered SP-13 retrieval server. The recall, why and dropped frontends
	// dispatch into it rather than re-implementing search, so the authorization, coverage and
	// fidelity distinctions its handlers make survive into the command output instead of being
	// re-derived — and re-derived differently — here.
	MCP mcp.Server
	// EvalArtifacts supplies a completed evaluation's artifacts. Running the replay harness is
	// test/replay's job; a command that re-ran it would be a second driver with its own corpus
	// selection and its own idea of what a trial is.
	EvalArtifacts EvalArtifacts
	// Status are the sources the status command collects from, in preference order. Both members
	// may be nil, which the report states rather than treating as an absence of trouble.
	Status StatusSources
	// Clock is the injected time source. A nil Clock means the system clock: a command is not
	// worth failing over a missing seam, and every caller that cares about determinism — every
	// test, every golden fixture — sets it.
	Clock core.Clock
	// Refused, when non-nil, is why this invocation has no project to act on at all: owner decision
	// D18 refuses a project root that is the user's home directory. Every command that reads or
	// writes a project then reports it as unavailable instead of running (refusalExempt names the
	// two that do not), and the error keeps its own identity under errors.Is, so a caller can still
	// branch on paths.ErrHomeRoot.
	Refused error
}

// refusalExempt are the commands that still run when Deps.Refused is set. status reports the
// refusal as its answer (Status.Refused), because "why is nothing recorded here" is the question it
// exists to answer; eval reads a completed evaluation's artifacts and never a project.
var refusalExempt = map[string]bool{"status": true, "eval": true}

// now reads the injected clock, defaulting to the system one.
func (d Deps) now() time.Time {
	if d.Clock == nil {
		return core.SystemClock().Now()
	}
	return d.Clock.Now()
}

// commandNames is the shipped §5.17 list, in the order `/qompack:help` should present them: the
// three a user reaches for daily first, then the two that explain what happened, then the harness.
//
// §5.17's checkpoint is not here. Its only route would be `qompack checkpoint`, which is the
// PreCompact hook entry point — it reads a hook event from stdin and exits 0 whatever happens — so
// the command wrote nothing. Checkpoints are sealed automatically before every compaction and on the
// checkpointer's cadence; internal/pluginmanifest's commandSpecs records why no manual route ships.
var commandNames = []string{"status", "recall", "pin", "why", "dropped", "eval"}

// All returns one Command per §5.17 name.
//
// The list is complete from wave 0 and its length never changes; SP-14 replaces bodies, not
// entries. That is what lets plugin/commands/*.md — which is generated from a typed source and
// diffed in CI — be written once and stay correct.
func All(d Deps) []Command {
	specs := Specs()
	cmds := make([]Command, 0, len(specs))
	for _, s := range specs {
		cmds = append(cmds, &frontend{spec: s, deps: d, body: bodyFor(s.Name)})
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

// Invocation is one parsed command line, handed to a body after dispatch has taken the common
// surface — help, --json, and the flag table the Spec documents — off the front of it.
type Invocation struct {
	// Spec is the command being run.
	Spec Spec
	// Deps is the late-bound dependency set. Members may be nil.
	Deps Deps
	// Args are the positional arguments, with every parsed flag removed.
	Args []string
	// Flags holds each documented flag that was present, by name. A boolean flag maps to "".
	Flags map[string]string
	// JSON is true when the caller asked for the machine-readable envelope.
	JSON bool
	// Now is the invocation time, read once from the injected clock so every rendered timestamp
	// in one command's output agrees.
	Now time.Time
	// Out is where a body writes its human-readable rendering. A body that only fills the
	// envelope Data member leaves it untouched.
	Out io.Writer
}

// Flag returns the value of a documented flag and whether it was present.
func (in Invocation) Flag(name string) (string, bool) {
	v, ok := in.Flags[name]
	return v, ok
}

// body is one command's real implementation. It returns the envelope Data member; anything it
// writes to inv.Out is the human rendering. A nil Data with a nil error is a legal "nothing to
// report".
type body func(ctx context.Context, inv Invocation) (json.RawMessage, error)

// bodyFor returns the implementation for name.
//
// Every §7.5 name resolves to something from wave 0 on: an unimplemented one resolves to
// notImplemented, which reports core.ErrNotImplemented naming the command. SP-14 replaces entries
// here one commit at a time, so a half-landed wave has some commands answering and the rest
// saying honestly that they do not.
func bodyFor(name string) body {
	switch name {
	case "status":
		return statusBody
	case "recall":
		return recallBody
	case "why":
		return whyBody
	case "dropped":
		return droppedBody
	case "pin":
		return pinBody
	case "eval":
		return evalBody
	default:
		return notImplemented(name)
	}
}

// notImplemented is the body every command carries until its own commit lands.
func notImplemented(name string) body {
	return func(context.Context, Invocation) (json.RawMessage, error) {
		return nil, fmt.Errorf("%w: /qompack:%s (SP-14)", core.ErrNotImplemented, name)
	}
}

// frontend is the dispatch shim in front of every body: it parses the documented flag table,
// answers --help, and renders either what the body wrote or the JSON envelope.
type frontend struct {
	spec Spec
	deps Deps
	body body
}

func (f *frontend) Name() string { return f.spec.Name }

// Run parses args against the Spec flag table and runs the body.
//
// The error it returns keeps its original identity — core.ErrNotImplemented stays
// core.ErrNotImplemented — because callers branch on it and the §2.3 exit-code mapping reads it.
// Under --json the same error is ALSO written into the envelope error member, so a scripted caller
// reading stdout learns what a person reading stderr would.
func (f *frontend) Run(ctx context.Context, args []string, out io.Writer) error {
	inv, help, err := f.parse(args, out)
	if err != nil {
		return f.report(inv, nil, err)
	}
	if help {
		return f.spec.WriteHelp(out)
	}
	if f.deps.Refused != nil && !refusalExempt[f.spec.Name] {
		return f.report(inv, nil, fmt.Errorf("%w: %w", ErrUnavailable, f.deps.Refused))
	}

	data, runErr := f.body(ctx, inv)
	return f.report(inv, data, runErr)
}

// report writes the output in the requested form and returns runErr unchanged.
func (f *frontend) report(inv Invocation, data json.RawMessage, runErr error) error {
	if !inv.JSON {
		return runErr
	}
	env := NewEnvelope(f.spec.Name)
	env.Data = data
	if runErr != nil {
		env.FailErr(runErr)
	}
	if err := writeEnvelope(inv.Out, env); err != nil && runErr == nil {
		return err
	}
	return runErr
}

// writeEnvelope emits env as one indented JSON document with a trailing newline.
func writeEnvelope(w io.Writer, env *Envelope) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(env)
}

// parse splits args into the documented flags and the positional remainder.
//
// It accepts `--flag value` and `--flag=value`, and both `--flag` and `-flag`, matching the stdlib
// flag package a user has already met in `qompack config print`. An undocumented flag is a usage
// error rather than a positional argument: silently treating `--jsonn` as a search term is how a
// user comes to believe recall is broken.
func (f *frontend) parse(args []string, out io.Writer) (Invocation, bool, error) {
	inv := Invocation{
		Spec:  f.spec,
		Deps:  f.deps,
		Flags: map[string]string{},
		Now:   f.deps.now(),
		Out:   out,
	}

	known := make(map[string]FlagSpec, len(f.spec.Flags))
	for _, fs := range f.spec.Flags {
		known[fs.Name] = fs
	}

	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			inv.Args = append(inv.Args, args[i+1:]...)
			break
		}
		if !strings.HasPrefix(a, "-") || a == "-" {
			inv.Args = append(inv.Args, a)
			continue
		}

		name, value, hasValue := strings.Cut(strings.TrimLeft(a, "-"), "=")

		if name == "h" || name == "help" {
			return inv, true, nil
		}

		spec, ok := known[name]
		if !ok {
			return inv, false, UsageErrorf("qompack %s: unknown flag %q", f.spec.Subcommand, a)
		}
		if spec.Arg != "" && !hasValue {
			if i+1 >= len(args) {
				return inv, false, UsageErrorf("qompack %s: --%s requires a <%s>",
					f.spec.Subcommand, name, spec.Arg)
			}
			i++
			value = args[i]
		}
		inv.Flags[name] = value
	}

	_, inv.JSON = inv.Flags[jsonFlag.Name]
	return inv, false, nil
}

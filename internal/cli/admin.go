package cli

import (
	"context"
	"flag"
	"fmt"
	"io"

	"github.com/qompack/qompack/internal/commands"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/daemon"
)

// The `admin` subcommands: operator tools that work on a STOPPED project.
//
// They are not hooks and not part of a session. Each one takes the daemon lock and refuses while a
// daemon owns it, so the exit-code policy of §2.3 applies in full: 0 when the operation succeeded,
// 2 for an invocation the operator got wrong, 1 for anything else.

// adminCmds is the admin subcommand table.
//
// `delivery-seal` is design §4.5's offline tool. The name under the admin surface is the design's;
// SP-17, which owns the operator surface, settles its final spelling.
func adminCmds() []Cmd {
	return []Cmd{{
		Name:    "admin delivery-seal",
		Summary: "check or convert the delivery journals' position seals (the daemon must be stopped)",
		Run:     runAdminDeliverySeal,
	}}
}

// errUsageReported is a usage error whose message the command has already written itself.
//
// Dispatch prints an error unless it is errAlreadyReported, and maps commands.ErrUsage to exit 2.
// A flag package that has already printed its own complaint wants both: no second message, and the
// exit code that says "you typed this wrong" rather than "it failed".
var errUsageReported = fmt.Errorf("%w: %w", errAlreadyReported, commands.ErrUsage)

// runAdminDeliverySeal implements
// `qompack admin delivery-seal [--project <root>] (--check | --to v1) [--accept-torn-slot --yes]`.
//
// The flag parsing, the mutual exclusion and the confirmation are decided here, because a usage
// error is the front end's to report; everything that touches the project belongs to
// daemon.RepairDeliverySeal, which validates its own options again for any other caller.
func runAdminDeliverySeal(_ context.Context, env Env, args []string, out, errw io.Writer) error {
	fs := flag.NewFlagSet("admin delivery-seal", flag.ContinueOnError)
	fs.SetOutput(errw)
	project := fs.String("project", "",
		"project root (defaults to QOMPACK_PROJECT_ROOT or the process cwd's nearest .git)")
	check := fs.Bool("check", false,
		"read both seals and both journals through the full reader and report; writes nothing")
	to := fs.String("to", "", `convert both seals once they load; the only value is "v1"`)
	tornSlot := fs.Bool("accept-torn-slot", false,
		"accept a seal with one valid slot beside one torn slot, when the journal holds a complete "+
			"canonical tail past the valid record; needs --yes")
	yes := fs.Bool("yes", false, "confirm --accept-torn-slot, which accepts a position the daemon refuses")
	if err := fs.Parse(args); err != nil {
		return errUsageReported // flag has already written its complaint and the usage to errw.
	}

	switch {
	case fs.NArg() > 0:
		fmt.Fprintf(errw, "qompack admin delivery-seal: unexpected argument %q\n", fs.Arg(0))
		return errUsageReported
	case *check == (*to != ""):
		fmt.Fprintln(errw, "qompack admin delivery-seal: pass exactly one of --check and --to v1")
		return errUsageReported
	case *to != "" && *to != "v1":
		fmt.Fprintf(errw, "qompack admin delivery-seal: --to accepts only \"v1\", got %q\n", *to)
		return errUsageReported
	case *tornSlot && !*yes:
		fmt.Fprintln(errw, "qompack admin delivery-seal: --accept-torn-slot accepts a position the "+
			"daemon's own reader refuses; pass --yes to confirm it")
		return errUsageReported
	}

	root := *project
	if root == "" {
		root = resolveProjectRoot(env, nil)
	}
	if root == "" {
		fmt.Fprintln(errw, "qompack admin delivery-seal: could not resolve a project root")
		return errAlreadyReported
	}
	clk := env.Clock
	if clk == nil {
		clk = core.SystemClock()
	}

	if err := daemon.RepairDeliverySeal(daemon.DeliverySealOptions{
		ProjectRoot:    root,
		Check:          *check,
		ToV1:           *to == "v1",
		AcceptTornSlot: *tornSlot,
		Confirm:        *yes,
		Out:            out,
		Clock:          clk,
	}); err != nil {
		fmt.Fprintf(errw, "qompack admin delivery-seal: %v\n", err)
		return errAlreadyReported
	}
	return nil
}

// Package cli is the binary's subcommand dispatch: the composition root that turns os.Args into
// one Cmd invocation, owns the exit-code policy of 00-ARCHITECTURE.md §2.3, and guarantees the
// single hardest rule in that section — a hook subcommand exits 0 no matter what happens inside it.
//
// A non-zero hook exit surfaces noise to the user and, for some hooks, can block the turn. Qompack
// is a sidecar (§7.1): it must never be the reason a session gets worse. So every failure a hook
// can reach — unreadable stdin, malformed JSON, an unresolvable project root, a read-only store, a
// panic in the handler — is logged and swallowed, and the host still receives valid JSON on stdout.
//
// cli is a composition root (§3.2): it may import anything, and nothing may import it.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/qompack/qompack/internal/core"
)

// Exit codes, per the §2.3 policy table. Only self-test and non-hook subcommands may fail.
const (
	// ExitOK is success, and is also the ONLY code a hook subcommand may ever return.
	ExitOK = 0
	// ExitError is a non-hook subcommand that failed, or a self-test assertion failure.
	ExitError = 1
	// ExitUsage is an unknown subcommand or a malformed flag.
	ExitUsage = 2
)

// Cmd is one subcommand.
type Cmd struct {
	// Name is the subcommand as typed, including a space for two-word forms ("observe tool").
	Name string
	// Summary is one line for `qompack help`.
	Summary string
	// Hook marks a subcommand invoked by the host as a hook. Hook commands must ALWAYS exit 0
	// (§2.3), which Dispatch enforces regardless of what Run returns or panics with.
	Hook bool
	// Run executes the subcommand. Its error is reported only for non-hook commands.
	Run func(ctx context.Context, env Env, args []string, out, errw io.Writer) error
}

// Env is everything a subcommand needs from the process, injected rather than read from globals so
// that every command is testable in-process without touching the real environment (§6.2).
type Env struct {
	Getenv func(string) string
	Stdin  io.Reader
	// Set accumulates --set dotted.key=value flags, the highest-precedence config layer (§11.2).
	Set   map[string]string
	Clock core.Clock
	// HomeDir is the user-global layer's root. Empty means "resolve from the environment".
	HomeDir string
	// Self is the running executable's own path (os.Executable()), used by the hook bodies and
	// self-test to launch a detached daemon (internal/daemon.SpawnDetached/EnsureRunning). Only
	// cmd/qompack/main.go — the real production entry point — ever sets it; every Env built by a
	// test (or any other composition root, e.g. test/guards' in-process write-set guard,
	// internal/testutil's RunHook) leaves it at its zero value "", which disables lazy spawn
	// entirely rather than failing (ipc.ClientOptions.Self's own documented contract). This is an
	// injected-dependency field, not a branch on "am I under test" (fix round 1, Important I-7):
	// the previous design read os.Executable() unconditionally and gated it on
	// testing.Testing(), which linked the stdlib testing/runtime/trace packages into the shipped
	// binary for no functional benefit — verify with
	// `go list -deps ./cmd/qompack | grep -c '^testing$'` == 0.
	Self string
}

// Dispatch routes argv to a command and returns the process exit code.
//
// argv is the raw os.Args, including argv[0]. Two-word subcommands ("observe tool", "config print")
// are matched before one-word ones, so `observe` alone is a usage error rather than a silent no-op.
func Dispatch(ctx context.Context, cmds []Cmd, argv []string, env Env, out, errw io.Writer) int {
	if len(argv) < 2 {
		writeUsage(cmds, out)
		return ExitOK
	}

	switch argv[1] {
	case "-h", "--help", "help":
		writeUsage(cmds, out)
		return ExitOK
	}

	cmd, rest, ok := match(cmds, argv[1:])
	if !ok {
		fmt.Fprintf(errw, "qompack: unknown subcommand %q\n\n", strings.Join(argv[1:], " "))
		writeUsage(cmds, errw)
		return ExitUsage
	}

	rest, setFlags, err := extractSetFlags(rest)
	if err != nil {
		// A malformed --set is a usage error for an ordinary command, but a hook must still
		// exit 0: the host gave us the arguments, and failing the turn over them helps nobody.
		fmt.Fprintf(errw, "qompack %s: %v\n", cmd.Name, err)
		if cmd.Hook {
			return ExitOK
		}
		return ExitUsage
	}
	if env.Set == nil {
		env.Set = map[string]string{}
	}
	for k, v := range setFlags {
		env.Set[k] = v
	}
	if env.Clock == nil {
		env.Clock = core.SystemClock()
	}

	err = runGuarded(ctx, cmd, env, rest, out, errw)

	if cmd.Hook {
		return ExitOK
	}
	if err != nil {
		if !errors.Is(err, errAlreadyReported) {
			fmt.Fprintf(errw, "qompack %s: %v\n", cmd.Name, err)
		}
		return ExitError
	}
	return ExitOK
}

// errAlreadyReported lets a command signal that it has written its own diagnostic and Dispatch
// should not print the error a second time.
var errAlreadyReported = errors.New("qompack: error already reported")

// match finds the longest-prefix command for args. Two-word names win over one-word names so
// "observe tool" is never mistaken for a command named "observe" with an argument.
func match(cmds []Cmd, args []string) (Cmd, []string, bool) {
	if len(args) >= 2 {
		two := args[0] + " " + args[1]
		for _, c := range cmds {
			if c.Name == two {
				return c, args[2:], true
			}
		}
	}
	for _, c := range cmds {
		if c.Name == args[0] {
			return c, args[1:], true
		}
	}
	return Cmd{}, nil, false
}

// extractSetFlags pulls every --set key=value (and --set=key=value) out of args, leaving the rest
// for the subcommand's own flag set. --set is accepted by every subcommand, so it is parsed here
// rather than repeated in each one.
func extractSetFlags(args []string) (rest []string, set map[string]string, err error) {
	set = map[string]string{}
	for i := 0; i < len(args); i++ {
		a := args[i]

		var kv string
		switch {
		case a == "--set":
			if i+1 >= len(args) {
				return nil, nil, fmt.Errorf("--set requires a dotted.key=value argument")
			}
			i++
			kv = args[i]
		case strings.HasPrefix(a, "--set="):
			kv = strings.TrimPrefix(a, "--set=")
		default:
			rest = append(rest, a)
			continue
		}

		k, v, found := strings.Cut(kv, "=")
		if !found || k == "" {
			return nil, nil, fmt.Errorf("--set expects dotted.key=value, got %q", kv)
		}
		set[k] = v
	}
	return rest, set, nil
}

// writeUsage prints the subcommand table, hooks last since a user never types those.
func writeUsage(cmds []Cmd, w io.Writer) {
	fmt.Fprintf(w, "qompack %s — cache-aware, retrieval-backed context compaction\n\n", core.Version)
	fmt.Fprintln(w, "usage: qompack <subcommand> [flags]")
	fmt.Fprintln(w, "\ncommands:")

	ordinary := make([]Cmd, 0, len(cmds))
	hooks := make([]Cmd, 0, len(cmds))
	for _, c := range cmds {
		if c.Hook {
			hooks = append(hooks, c)
			continue
		}
		ordinary = append(ordinary, c)
	}
	sort.Slice(ordinary, func(i, j int) bool { return ordinary[i].Name < ordinary[j].Name })
	sort.Slice(hooks, func(i, j int) bool { return hooks[i].Name < hooks[j].Name })

	for _, c := range ordinary {
		fmt.Fprintf(w, "  %-16s %s\n", c.Name, c.Summary)
	}
	fmt.Fprintln(w, "\nhook entry points (invoked by Claude Code, always exit 0):")
	for _, c := range hooks {
		fmt.Fprintf(w, "  %-16s %s\n", c.Name, c.Summary)
	}
	fmt.Fprintf(w, "\nany subcommand accepts --set <dotted.key>=<value> to override configuration.\n")
}

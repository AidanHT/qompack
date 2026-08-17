package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/paths"
)

// All returns the complete subcommand table.
//
// Every subcommand §2.3 names is present from wave 0, including the ones no wave has implemented:
// they are registered with a body that reports core.ErrNotImplemented. That is deliberate. A
// complete table means a later subplan replaces one function value rather than editing dispatch,
// `qompack help` tells the truth about the binary's surface today, and the six hook entry points
// are wired end to end before any of the machinery behind them exists.
func All() []Cmd {
	cmds := hookCmds()
	cmds = append(cmds,
		Cmd{Name: "version", Summary: "print the plugin version", Run: runVersion},
		Cmd{Name: "config print", Summary: "print the effective configuration", Run: runConfigPrint},
		Cmd{Name: "config schema", Summary: "print the configuration JSON Schema", Run: runConfigSchema},
		Cmd{Name: "daemon", Summary: "run the resident per-project daemon", Run: runDaemon},
		Cmd{Name: "self-test", Summary: "assert every host contract; the only command that may exit non-zero", Run: runSelfTest},
	)
	cmds = append(cmds, evalCmds()...)
	for _, ni := range notImplemented {
		cmds = append(cmds, Cmd{Name: ni.name, Summary: ni.summary, Run: notImplementedRun(ni.name)})
	}
	return cmds
}

// notImplemented is every §2.3 subcommand whose owning subplan has not merged. Keeping the list as
// data — rather than as absent entries — is what makes the dispatch table complete on day one.
var notImplemented = []struct{ name, summary string }{
	{"mcp", "run the MCP server over stdio (SP-13)"},
	{"status", "mode, contracts, store, latency, last decision (SP-14)"},
	{"recall", "search stored tool output and file versions (SP-14)"},
	{"pin", "pin an invariant so it is never summarized away (SP-14)"},
	{"why", "explain a recorded decision and its evidence (SP-14)"},
	{"dropped", "report what the last compaction dropped (SP-14)"},
	{"eval", "run the replay harness and score it (SP-02)"},
	{"fsck", "verify store and checkpoint integrity (SP-17)"},
	{"doctor", "diagnose installation and host contract problems (SP-17)"},
	{"bench", "run the hot-path latency harness (SP-05)"},
}

// notImplementedRun reports an unimplemented subcommand honestly: a clear message naming the
// build, and core.ErrNotImplemented so a caller can branch on it.
func notImplementedRun(name string) func(context.Context, Env, []string, io.Writer, io.Writer) error {
	return func(_ context.Context, _ Env, _ []string, _ io.Writer, errw io.Writer) error {
		fmt.Fprintf(errw, "qompack %s: not implemented in this build\n", name)
		return fmt.Errorf("%w: %s", errAlreadyReported, core.ErrNotImplemented)
	}
}

// runVersion prints the version core.Version carries, which SP-17 overrides at link time.
func runVersion(_ context.Context, _ Env, _ []string, out, _ io.Writer) error {
	_, err := fmt.Fprintln(out, core.Version)
	return err
}

// loadForCommand resolves the project root and loads configuration for a non-hook subcommand.
// Unlike a hook, an ordinary command may fail loudly: there is no turn to protect.
func loadForCommand(env Env) (config.Config, config.Provenance, error) {
	root, err := paths.Resolve(env.Getenv, "")
	if err != nil {
		cwd, cwdErr := currentDir()
		if cwdErr != nil {
			return config.Config{}, nil, fmt.Errorf("resolving project root: %w", err)
		}
		if root, err = paths.Resolve(env.Getenv, cwd); err != nil {
			return config.Config{}, nil, fmt.Errorf("resolving project root: %w", err)
		}
	}
	return LoadConfigAndReport(config.Env{
		ProjectRoot: root,
		HomeDir:     homeDir(env),
		Getenv:      env.Getenv,
		Flags:       env.Set,
	}, logging.Nop(), nil)
}

// runConfigPrint implements `qompack config print [--provenance] [--json]`. §11.4 makes this the
// first thing docs/troubleshooting.md tells a user to run, because it answers "what is actually in
// effect, and which of the five layers put it there".
func runConfigPrint(_ context.Context, env Env, args []string, out, errw io.Writer) error {
	fs := flag.NewFlagSet("config print", flag.ContinueOnError)
	fs.SetOutput(errw)
	withProvenance := fs.Bool("provenance", false, "annotate each leaf with where its value came from")
	asJSON := fs.Bool("json", false, "emit plain JSON with no annotations")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, prov, err := loadForCommand(env)
	if err != nil {
		return err
	}

	if *withProvenance && !*asJSON {
		return prov.Render(cfg, out)
	}

	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(cfg)
}

// runConfigSchema implements `qompack config schema`, the machine-readable counterpart to
// docs/config-reference.md.
func runConfigSchema(_ context.Context, env Env, _ []string, out, _ io.Writer) error {
	cfg, _, err := loadForCommand(env)
	if err != nil {
		// A schema is a property of the type, not of this project's values, so emit it from the
		// defaults rather than failing when there is no project to resolve.
		cfg = config.Defaults()
	}
	b := cfg.JSONSchema()
	if _, err := out.Write(b); err != nil {
		return err
	}
	if len(b) > 0 && b[len(b)-1] != '\n' {
		_, err = fmt.Fprintln(out)
	}
	return err
}

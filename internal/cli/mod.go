package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/contract"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/modwire"
	"github.com/qompack/qompack/internal/paths"
)

// The `qompack mod <verb>` group is the binary's half of the bridge to Qompack's Claude Code mod
// (docs/adr/0015-claude-code-mods-integration.md). The mod runs the bundled binary through the
// host's process API with one JSON object on stdin and reads one JSON object from stdout; both are
// internal/modwire's types. Every handled failure is an unserved response with a reason and exit 0,
// so the mod only ever has to read one object. The verbs answer the mod, not the host, so they are
// ordinary commands, not hook entry points, and their output is never hookio's.

// modCmds returns the `qompack mod` verbs.
func modCmds() []Cmd {
	return []Cmd{
		{
			Name:    "mod hello",
			Summary: "the Claude Code mod's session announcement (JSON on stdin and stdout)",
			Run:     runModVerb(modwire.VerbHello, modHello),
		},
	}
}

// modVerb answers one verb: it reads the request from stdin and returns the response to write,
// which is always one of the verb's modwire response types or a bare modwire.Status.
type modVerb func(ctx context.Context, env Env, stdin io.Reader) any

// runModVerb wraps a verb in the group's framing: no arguments beyond --set (which Dispatch has
// already taken), the request on stdin, exactly one response object on stdout, and a nil error for
// every handled failure, so Dispatch exits 0.
func runModVerb(verb modwire.Verb, answer modVerb) func(context.Context, Env, []string, io.Writer, io.Writer) error {
	return func(ctx context.Context, env Env, args []string, out, _ io.Writer) error {
		var resp any
		if len(args) > 0 {
			resp = modwire.Failure(modwire.ReasonError,
				fmt.Sprintf("qompack mod %s takes its request on stdin, not arguments", verb))
		} else {
			stdin := env.Stdin
			if stdin == nil {
				stdin = strings.NewReader("")
			}
			resp = answer(ctx, env, stdin)
		}
		return writeModResponse(out, resp)
	}
}

// writeModResponse writes resp as one line. A response that cannot be encoded is replaced by the
// error object that says why, so stdout always carries exactly one object.
func writeModResponse(out io.Writer, resp any) error {
	b, err := modwire.Encode(resp)
	if err != nil {
		if b, err = modwire.Encode(modwire.FailureFor(err)); err != nil {
			return err
		}
	}
	_, err = out.Write(b)
	return err
}

// The refusal details. They name the condition, never a path or anything from the request.
const (
	modRefusedHomeRoot = "the project root is the home directory"
	modRefusedNoStore  = "the project has no .qompack store"
	modRefusedOff      = "runtime.mod.enabled is false"
	modRefusedModeOff  = "runtime.mode is off"
)

// modFailure is modwire.FailureFor with the absolute paths taken out of the detail, which reaches
// the host's debug log: the project root becomes "." and the home directory "~", in each spelling
// this process may have given them to the filesystem (as resolved, in slash form, and in the
// long-path form paths.Long adds). The longest spelling is replaced first, so a root inside the
// home reads as ".", not "~/...". Errors from the filesystem and the configuration loader name
// paths under these two directories only.
func modFailure(err error, root, home string) modwire.Status {
	type sub struct{ from, to string }
	var subs []sub
	for _, d := range []sub{{root, "."}, {home, "~"}} {
		if d.from == "" {
			continue
		}
		for _, form := range []string{paths.Long(d.from), d.from, filepath.ToSlash(d.from)} {
			subs = append(subs, sub{form, d.to})
		}
	}
	sort.SliceStable(subs, func(i, j int) bool { return len(subs[i].from) > len(subs[j].from) })
	msg := err.Error()
	for _, s := range subs {
		msg = strings.ReplaceAll(msg, s.from, s.to)
	}
	return modwire.FailureFor(errors.New(msg))
}

// modServedProject decides whether a mod request for cwd is served, and returns the project root
// and its configuration when it is.
//
// The root is resolved exactly as the hooks resolve it (resolveProjectRoot: QOMPACK_PROJECT_ROOT,
// else the nearest .git above cwd, else cwd), from the session root the mod sends. A request is
// refused (refusal non-empty) for a home-directory root, before anything is read from it (D18,
// isHomeRoot); for a project with no .qompack store, which is never created for it; when
// runtime.mod.enabled is false; and when the project's runtime mode is off. The switch is checked
// before the mode because it is off by default and the mode's state.bin read can probe a daemon. A
// configuration that does not load is an error, not a refusal. Nothing is written on any of these
// paths.
//
// The mode is the one the hooks and the command clients work under (modeFor): off when the
// configuration says off, and off when state.bin says off while the daemon that wrote it is alive.
// A state.bin left behind by a daemon that died defers to the configuration, so it cannot keep the
// mod refused after runtime.mode is set back (the hooks' stateModeOffHolds, D67(c)). Reading
// state.bin, and probing the daemon only when it says off, writes nothing.
func modServedProject(env Env, cwd string) (root string, cfg config.Config, refusal string, err error) {
	root = resolveProjectRoot(env, &hookio.Event{CWD: cwd})
	if isHomeRoot(env, root) {
		return root, config.Config{}, modRefusedHomeRoot, nil
	}
	if !isDir(paths.Of(root).Dot) {
		return root, config.Config{}, modRefusedNoStore, nil
	}
	cfg, _, _, err = config.Load(config.Env{
		ProjectRoot: root, HomeDir: homeDir(env), Getenv: env.Getenv, Flags: env.Set,
	})
	if err != nil {
		return root, config.Config{}, "", fmt.Errorf("configuration: %w", err)
	}
	// The switch first: it is off by default, and reading the mode can probe a daemon.
	if !cfg.Runtime.Mod.Enabled {
		return root, cfg, modRefusedOff, nil
	}
	if modeFor(root, ipc.ReadState(root, cfg).Mode, cfg) == contract.ModeOff {
		return root, cfg, modRefusedModeOff, nil
	}
	return root, cfg, "", nil
}

// modHello is `qompack mod hello`. A served hello writes the session's hello file and answers
// whether the mod's features are on for this host (modwire.MeetsMinHostVersion); a host below the
// minimum is still served, so its version is on record. A refused hello writes nothing. Hello needs
// no daemon: the file is written from this process.
func modHello(_ context.Context, env Env, stdin io.Reader) any {
	req, err := modwire.DecodeHelloRequest(stdin)
	if err != nil {
		return modwire.FailureFor(err)
	}
	unserved := func(st modwire.Status) modwire.HelloResponse { return modwire.HelloResponse{Status: st} }

	root, _, refusal, err := modServedProject(env, req.CWD)
	switch {
	case err != nil:
		return unserved(modFailure(err, root, homeDir(env)))
	case refusal != "":
		return unserved(modwire.Failure(modwire.ReasonRefused, refusal))
	}

	clk := env.Clock
	if clk == nil {
		clk = core.SystemClock()
	}
	if err := writeHelloFile(root, modwire.HelloFile{
		SessionID: req.SessionID, HostVersion: req.HostVersion, TS: core.NowMilli(clk),
	}); err != nil {
		return unserved(modFailure(fmt.Errorf("hello file: %w", err), root, homeDir(env)))
	}
	resp := modwire.HelloResponse{Status: modwire.Served(), Enabled: modwire.MeetsMinHostVersion(req.HostVersion)}
	if err := resp.Validate(); err != nil {
		return unserved(modwire.FailureFor(err))
	}
	return resp
}

// writeHelloFile writes h as <root>/.qompack/state/mod-hello-<session>.json, replacing an earlier
// hello of the same session (a mod reload says hello again). The store exists (modServedProject),
// so only its state directory may need creating.
func writeHelloFile(root string, h modwire.HelloFile) error {
	name, err := modwire.HelloFileName(h.SessionID)
	if err != nil {
		return err
	}
	b, err := h.Encode()
	if err != nil {
		return err
	}
	dir := paths.Of(root).State
	if err := os.MkdirAll(paths.Long(dir), 0o700); err != nil {
		return err
	}
	return paths.WriteAtomic(filepath.Join(dir, name), b, 0o600)
}

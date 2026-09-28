package cli

import (
	"io"
	"slices"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/paths"
)

// Owner decision D18 (2026-09-26): a session whose project root resolves to the user's home
// directory is refused. paths.Resolve walks up to the nearest .git and otherwise takes the working
// directory, so a session started in the home directory, or anywhere below a home that is a git
// work tree (a dotfiles repository), resolves to the home directory — and its project store would
// be <home>/.qompack, the user-global layer's own directory (config.json, calibration.json, the
// fallback logs, the D10 staged daemon copies under bin/). Such a session records nothing and says
// why: the one host message below on SessionStart, the MCP tools' stable refusal, a reason in
// status and doctor, and a non-zero exit from every command that would otherwise touch a store.
//
// Every entry point that resolves a project root asks refuseHomeRoot about the root it resolved,
// before it reads state from, writes to, locks or spawns a daemon for that root. The comparison
// itself is paths.IsHome.

// homeRootNotice is the one message a refused session puts in front of the user: SessionStart's
// systemMessage. The other six hooks answer {}. It is one line and far under the host's
// 10,000-character field cap and D15's 1,000-character ceiling for a whole banner, and it says both
// why nothing is recorded and what to do about it.
const homeRootNotice = "Qompack is inactive in this session: its project root is your home directory, " +
	"so it records nothing. Open a project directory (one with its own .git) to use Qompack."

// homeDirsFor returns every directory the refusal treats as the user's home: HOME and USERPROFILE
// as env.Getenv reports them (paths.HomeDirs reads both on every platform, because a POSIX layer on
// Windows exports a HOME that the user-global layer's own loader prefers), and homeDir(env), the
// home this process's user-global layer actually uses — env.HomeDir when a caller injected one,
// else the environment, else os.UserHomeDir, which the daemon's own staging and reload use.
func homeDirsFor(env Env) []string {
	var dirs []string
	if env.Getenv != nil {
		dirs = paths.HomeDirs(env.Getenv)
	}
	if h := homeDir(env); h != "" && !slices.Contains(dirs, h) {
		dirs = append(dirs, h)
	}
	return dirs
}

// refuseHomeRoot returns nil for a root Qompack may use, or the D18 refusal, which wraps
// paths.ErrHomeRoot and names the root and the fix.
func refuseHomeRoot(env Env, root string) error {
	return paths.RefuseHome(root, homeDirsFor(env)...)
}

// isHomeRoot is refuseHomeRoot's question without the message, for the hooks, which never print
// one: a hook answers the host instead (writeRefusedHookOutput).
func isHomeRoot(env Env, root string) bool {
	return paths.IsHome(root, homeDirsFor(env)...)
}

// writeRefusedHookOutput answers a hook in a refused session: the notice on SessionStart and {}
// on every other event, reduced to what the host accepts for the event like any other answer. A
// refused hook reads nothing from the root, writes nothing, logs nothing and starts no daemon: the
// user-global layer's directory is not a project store, not even for a diagnostic.
func writeRefusedHookOutput(op ipc.Op, args []string, out io.Writer) error {
	o := hookio.Empty()
	if op == ipc.OpSessionStart {
		o = hookio.Output{SystemMessage: homeRootNotice}
	}
	return hookio.WriteOutput(out, hookio.ConformOutput(hookEvent(op, args), o))
}

// loadUserGlobalConfig loads the configuration for a refused root: defaults, the user-global file,
// the environment and --set flags, with no project layer, because the home directory has none. It
// reports nothing and persists nothing: LoadConfigAndReport would write state/config-violations.json
// under the root, which is the user-global layer's own directory.
//
// config.Load has no "no project layer" mode, so this names the user's home as both roots. The
// project file it then reads is the user file itself (both are <home>/.qompack/config.json), and
// applying one file twice is idempotent leaf by leaf, so the values are exactly the user layer's.
// Only the second pass's labels differ, and they are put back: every leaf that pass set is a
// user-file leaf, and every warning it raised repeats one the first pass raised.
func loadUserGlobalConfig(env Env) (config.Config, config.Provenance, []config.Warning, error) {
	home := homeDir(env)
	cfg, prov, warns, err := config.Load(config.Env{
		ProjectRoot: home, HomeDir: home, Getenv: env.Getenv, Flags: env.Set,
	})
	if err != nil {
		return cfg, prov, warns, err
	}
	for k, src := range prov {
		if src.Origin == config.OriginProjectFile {
			src.Origin = config.OriginUserFile
			prov[k] = src
		}
	}
	seen := make(map[config.Warning]bool, len(warns))
	unique := warns[:0]
	for _, w := range warns {
		if !seen[w] {
			seen[w] = true
			unique = append(unique, w)
		}
	}
	return cfg, prov, unique, nil
}

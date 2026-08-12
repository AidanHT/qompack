package cli

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/paths"
)

// hookLogPrefix names the per-day hook observation log. It records that a hook fired and how big
// its payload was — never any payload CONTENT, so the wave-0 no-op hooks are observable without
// storing anything a user would not expect us to store (§13 invariant 7).
const hookLogPrefix = "hooks-"

// hookLogDateLayout is the date stamp in the hook log's filename.
const hookLogDateLayout = "20060102"

// hookRecord is one line of .qompack/logs/hooks-YYYYMMDD.jsonl.
type hookRecord struct {
	TS        core.UnixMilli `json:"ts"`
	Hook      string         `json:"hook"`
	SessionID string         `json:"session_id"`
	ToolName  string         `json:"tool_name,omitempty"`
	Source    string         `json:"source,omitempty"`
	Bytes     int            `json:"bytes"`
	Truncated bool           `json:"truncated"`
}

// hookResponder produces the response a given hook writes once observation is done.
type hookResponder func() hookio.Output

// runHook is the shared body of all six no-op hook entry points.
//
// The step order is forced and is not the obvious one. The payload size limit is a config key
// (runtime.hotPath.maxPayloadBytes); config lives under the project root; and the project root
// comes out of the payload we have not read yet. That cycle is broken by reading stdin under the
// DEFAULT limit first: a hook must never need a config file in order to read its own stdin.
//
// Every step after the read is best-effort. A hook that cannot resolve a root, cannot load config,
// or cannot write its observation line still owes the host a valid response, so failures are
// logged and execution continues to the write.
func runHook(name string, respond hookResponder) func(context.Context, Env, []string, io.Writer, io.Writer) error {
	return func(_ context.Context, env Env, _ []string, out, _ io.Writer) error {
		clock := env.Clock
		if clock == nil {
			clock = core.SystemClock()
		}

		// 1. Read stdin under the DEFAULT limit. Deliberate: see the doc comment above.
		bootstrapLimit := int64(config.Defaults().Runtime.HotPath.MaxPayloadBytes)
		ev, raw, readErr := hookio.ReadEvent(env.Stdin, bootstrapLimit)
		truncated := errors.Is(readErr, core.ErrBudget)

		// 2. Resolve the project root (best effort). On failure, skip straight to the response:
		// with no root there is nowhere to write an observation, and the host is still owed JSON.
		//
		// The root must also already EXIST. paths.Resolve is faithful to §3.3, whose third and
		// last resort is "the payload cwd itself" — so a payload carrying a cwd that is not a real
		// directory resolves successfully to a path that is not a real directory. Without this
		// check the next step would MkdirAll a whole .qompack/ tree there, which is how a
		// malformed payload turns into a store in a location no project occupies. §3.3 says
		// .qompack/ lives at "the root of the project Claude Code is operating on"; a directory
		// that does not exist is not that. Refusing is safe: observation is best-effort and the
		// host still gets its response.
		if root, rootErr := paths.Resolve(env.Getenv, ev.CWD); rootErr == nil && isDir(root) {
			// 3–4. Load config, ensure the layout, append one observation line.
			observe(root, env, name, ev, len(raw), truncated, readErr, clock)
		}

		// 5. Always respond.
		return hookio.WriteOutput(out, respond())
	}
}

// observe loads configuration, ensures the layout exists, and appends one hook-observation line.
// Every failure inside is logged and swallowed: nothing here may stop the hook from responding.
func observe(root string, env Env, name string, ev hookio.Event, n int, truncated bool, readErr error, clock core.Clock) {
	l := paths.Of(root)
	if err := paths.EnsureLayout(l); err != nil {
		return
	}

	log, closer, err := logging.New(l.Logs, logging.Info)
	if err != nil {
		log = logging.Nop()
	} else {
		defer func() { _ = closer.Close() }()
	}

	cfg, _, cfgErr := LoadConfigAndReport(config.Env{
		ProjectRoot: root,
		HomeDir:     homeDir(env),
		Getenv:      env.Getenv,
		Flags:       env.Set,
	}, log, nil)
	if cfgErr != nil {
		cfg = config.Defaults()
	}

	// If the EFFECTIVE limit is smaller than what we already read under the bootstrap limit, say
	// so and move on. Never re-read (stdin is consumed), never fail (§2.3).
	if limit := cfg.Runtime.HotPath.MaxPayloadBytes; n > limit {
		log.Warn("hook payload exceeds the configured limit",
			"hook", name, "bytes", n, "limit", limit)
		truncated = true
	}
	if readErr != nil {
		log.Warn("hook payload did not parse cleanly", "hook", name, "err", readErr.Error())
	}

	rec := hookRecord{
		TS:        core.NowMilli(clock),
		Hook:      name,
		SessionID: string(ev.SessionID),
		ToolName:  ev.ToolName,
		Source:    ev.Source,
		Bytes:     n,
		Truncated: truncated,
	}
	p := filepath.Join(l.Logs, hookLogPrefix+clock.Now().UTC().Format(hookLogDateLayout)+".jsonl")
	if err := paths.AppendJSONL(p, rec); err != nil {
		log.Warn("could not append hook observation", "hook", name, "err", err.Error())
	}
}

// homeDir resolves the user-global layer's home, preferring an explicitly injected value so tests
// never touch the real home directory.
func homeDir(env Env) string {
	if env.HomeDir != "" {
		return env.HomeDir
	}
	if env.Getenv != nil {
		for _, k := range []string{"HOME", "USERPROFILE"} {
			if v := env.Getenv(k); v != "" {
				return v
			}
		}
	}
	h, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return h
}

// hookCmds returns the six no-op hook entry points of §7.3. Each observes and responds; SP-08
// (observer), SP-10 (checkpointer) and SP-11 (rehydrator) replace the bodies behind them, and
// SP-05 replaces the transport underneath. The response shapes are already correct, so a host
// wired to this build sees exactly the traffic it will see from the finished plugin.
func hookCmds() []Cmd {
	return []Cmd{
		{
			Name: "observe tool", Hook: true,
			Summary: "PostToolUse — chunk and store tool results (SP-08)",
			Run:     runHook("PostToolUse", hookio.Empty),
		},
		{
			Name: "observe prompt", Hook: true,
			Summary: "UserPromptSubmit — capture user intent verbatim (SP-08)",
			Run:     runHook("UserPromptSubmit", hookio.Empty),
		},
		{
			Name: "observe stop", Hook: true,
			Summary: "Stop/SubagentStop — capture subagent detail (SP-08)",
			Run:     runHook("Stop", hookio.Empty),
		},
		{
			Name: "session-start", Hook: true,
			Summary: "SessionStart — load or rehydrate the store (SP-08/SP-11)",
			Run:     runHook("SessionStart", func() hookio.Output { return hookio.SessionStartOutput("") }),
		},
		{
			Name: "checkpoint", Hook: true,
			Summary: "PreCompact — write an immutable checkpoint (SP-10)",
			Run:     runHook("PreCompact", func() hookio.Output { return hookio.PreCompactOutput("") }),
		},
		{
			Name: "flush", Hook: true,
			Summary: "SessionEnd — flush, compact, write the session index (SP-08)",
			Run:     runHook("SessionEnd", hookio.Empty),
		},
	}
}

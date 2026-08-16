// Command qompack is the single static binary that is the entire plugin runtime: hook entry
// points, the MCP server, the resident daemon, and the user-facing subcommands.
//
// It is dispatch only, under 150 lines, with no package-level initialization beyond variable
// declarations. That restraint is a latency requirement, not a style preference: this binary is
// spawned on the hot path of every tool call, and budget B-A (§2.4) allows 15 ms p99 for the whole
// process — spawn, connect, write, acknowledge, exit. Work done in init() is work every hook pays.
package main

import (
	"context"
	"os"

	"github.com/qompack/qompack/internal/cli"
	"github.com/qompack/qompack/internal/core"
)

func main() {
	// Best-effort: an error here means "cannot self-locate", which cli.Env.Self's own contract
	// already treats as "disable lazy spawn" rather than a fatal condition.
	self, _ := os.Executable()

	env := cli.Env{
		Getenv: os.Getenv,
		Stdin:  os.Stdin,
		Clock:  core.SystemClock(),
		Self:   self,
	}
	os.Exit(cli.Dispatch(context.Background(), cli.All(), os.Args, env, os.Stdout, os.Stderr))
}

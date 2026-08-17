package cli

import (
	"context"
	"io"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/eval"
)

// evalCmds is the `qompack eval …` verb group.
//
// It lives in its own file so the L7 subplan adds a command without editing the shared table
// beyond the one line in All() that splices it in. Only the `import` verb exists today; the bare
// `eval` entry stays in the not-implemented list, because running the replay harness from the
// binary is SP-14's `/qompack:eval`, not this.
func evalCmds() []Cmd {
	return []Cmd{{
		Name:    "eval import",
		Summary: "import recorded Claude Code transcripts as a redacted replay corpus (SP-02)",
		Run:     runEvalImport,
	}}
}

// runEvalImport adapts eval.ImportCommand — which returns a process exit code, because it is also
// callable from a tool — onto the cli.Cmd contract, which speaks in errors.
//
// Diagnostics go to the command's own writer, so the error returned here is errAlreadyReported:
// Dispatch must not print a second, less useful line on top of the one the importer already wrote.
func runEvalImport(_ context.Context, env Env, args []string, out, _ io.Writer) error {
	if code := eval.ImportCommand(args, out, config.Env{Getenv: env.Getenv}); code != 0 {
		return errAlreadyReported
	}
	return nil
}

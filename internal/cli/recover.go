package cli

import (
	"context"
	"fmt"
	"io"
	"runtime/debug"

	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/logging"
)

// runGuarded invokes cmd.Run with a panic barrier.
//
// §12.3 requires that any hook panic is recovered, logged, and turned into exit 0 with empty
// output. The subtle part is the "with empty output" half: if the handler panicked before writing
// its response, the host is left waiting on a stream that will never contain valid JSON. So for a
// hook command the deferred handler writes hookio.Empty() itself, and countingWriter is what tells
// it whether anything was written already — writing a second JSON document would be worse than
// writing none.
//
// The panic is reported through logging.Loud rather than the ordinary log, because §12 classes a
// crash in the hot path as a degradation and degradations are never silent.
func runGuarded(ctx context.Context, cmd Cmd, env Env, args []string, out, errw io.Writer) (err error) {
	cw := &countingWriter{w: out}

	defer func() {
		r := recover()
		if r == nil {
			return
		}
		stack := string(debug.Stack())

		// Loud goes to the day log, LOUD.log and the in-memory ring even when no file-backed
		// logger was ever constructed, so a panic before logger setup is still recorded.
		logging.Nop().Loud("panic in subcommand",
			"subcommand", cmd.Name, "recover", fmt.Sprint(r), "stack", stack)
		fmt.Fprintf(errw, "qompack %s: panic recovered: %v\n", cmd.Name, r)

		if cmd.Hook {
			if cw.n == 0 {
				_ = hookio.WriteOutput(cw, hookio.Empty())
			}
			err = nil // a hook never propagates failure; Dispatch maps it to exit 0 regardless
			return
		}
		err = fmt.Errorf("%w: panic: %v", errAlreadyReported, r)
	}()

	err = cmd.Run(ctx, env, args, cw, errw)

	// A hook that returned an error without writing a response still owes the host valid JSON.
	if cmd.Hook && cw.n == 0 {
		_ = hookio.WriteOutput(cw, hookio.Empty())
	}
	return err
}

// countingWriter counts bytes written so the panic barrier can tell whether a response already
// reached the host.
type countingWriter struct {
	w io.Writer
	n int
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += n
	return n, err
}

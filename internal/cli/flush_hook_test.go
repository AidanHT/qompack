package cli

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/pluginmanifest"
)

// TestFlushHook_IsFireAndForgetWithItsNonce is C1.15's client half. Claude Code gives a plugin's
// SessionEnd hooks one shared 1.5 s budget and cancels a hook still running when it runs out, so the
// flush hook asks the daemon only to take the flush durably — a fire-and-forget request, answered by
// an ACK once the line is in the WAL and leased — and never waits for the session's end, which the
// daemon runs on its own (internal/daemon/session_end.go). It carries its delivery nonce, so a copy it
// spools after a late ACK is absorbed through the flush's own lease instead of ending the session
// twice. An ACK in time leaves nothing in the client spool.
func TestFlushHook_IsFireAndForgetWithItsNonce(t *testing.T) {
	root := replyProject(t)
	got := make(chan ipc.Request, 1)
	replyDaemon(t, root, func(req ipc.Request) *hookio.Output {
		select {
		case got <- req:
		default:
		}
		return nil
	})

	stdout := runEntryPoint(t, root, pluginmanifest.HookEntryPoint{Event: "SessionEnd", Subcommand: "flush"})
	require.Equal(t, "{}\n", string(stdout), "SessionEnd discards every output field")

	var req ipc.Request
	select {
	case req = <-got:
	case <-time.After(5 * time.Second):
		require.FailNow(t, "the flush hook never reached the daemon")
	}
	require.Equal(t, ipc.OpFlush, req.Op)
	require.False(t, req.Reply, "the flush hook must not wait for the session's end; the host cancels it at 1.5 s")
	require.NotEmpty(t, req.Nonce, "the flush carries its delivery nonce, so a spooled copy is absorbed, not replayed")

	entries, err := os.ReadDir(paths.Long(paths.Of(root).Spool))
	require.NoError(t, err)
	for _, e := range entries {
		require.False(t, strings.HasPrefix(e.Name(), "client-"),
			"an ACK in time leaves nothing in the client spool: found %s", e.Name())
	}
}

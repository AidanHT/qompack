package cli

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/config"
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

	requireNoClientSpool(t, root, "an ACK in time leaves nothing in the client spool")
}

// flushSlowAck is how long the fake daemon below takes to acknowledge a flush. It is longer than the
// observe hot path's ACK deadline on every platform (config.AckDeadlineMs*: 73 ms at most), which
// the flush used to wait for, and far inside the host's shared 1.5 s SessionEnd budget.
const flushSlowAck = 200 * time.Millisecond

// TestFlushHook_ASlowAckInsideTheHostBudgetLeavesNoSpool: before the daemon ACKs a flush it does more
// than it does for an observe event — the WAL append and the lease, the in-process ownership and a
// rewrite of the session recovery record — and the host gives the SessionEnd hook far longer than the
// observe hot path's ACK deadline. A flush acknowledged inside that budget is delivered: it must not
// also be spooled, which costs a duplicate the daemon has to absorb for every session that ends.
func TestFlushHook_ASlowAckInsideTheHostBudgetLeavesNoSpool(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, paths.EnsureLayout(paths.Of(root)))
	st := ipc.StateFromConfig(config.Defaults())
	st.ConnectDeadlineMs = 5000 // the subject is the ACK wait, not the dial
	require.NoError(t, ipc.WriteState(root, st))
	shipped := time.Duration(st.AckDeadlineMs) * time.Millisecond
	require.Greater(t, flushSlowAck, 2*shipped,
		"precondition: the fake daemon's ACK is well past the hot path's shipped ACK deadline")
	require.LessOrEqual(t, flushSlowAck, flushAckDeadline/2,
		"precondition: and well inside the flush's own, so a loaded machine cannot turn it into a miss")

	got := make(chan ipc.Request, 1)
	replyDaemon(t, root, func(req ipc.Request) *hookio.Output {
		if req.Op == ipc.OpFlush {
			slow := time.NewTimer(flushSlowAck)
			<-slow.C
		}
		select {
		case got <- req:
		default:
		}
		return nil
	})

	runEntryPoint(t, root, pluginmanifest.HookEntryPoint{Event: "SessionEnd", Subcommand: "flush"})
	select {
	case req := <-got:
		require.Equal(t, ipc.OpFlush, req.Op)
	case <-time.After(5 * time.Second):
		require.FailNow(t, "the flush hook never reached the daemon")
	}
	requireNoClientSpool(t, root,
		"a flush the daemon acknowledged inside the host's budget was spooled as if it had been lost")
}

// requireNoClientSpool fails if any hook left a client spool under root.
func requireNoClientSpool(t *testing.T, root, why string) {
	t.Helper()
	entries, err := os.ReadDir(paths.Long(paths.Of(root).Spool))
	require.NoError(t, err)
	for _, e := range entries {
		require.False(t, strings.HasPrefix(e.Name(), "client-"), "%s: found %s", why, e.Name())
	}
}

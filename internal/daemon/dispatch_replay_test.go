package daemon

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/ipc"
)

// TestDispatchOp_AReplayIsNotAServedRequest: dispatchOp's noteServed releases Run's one re-drain once
// the daemon has provably accepted a connection (redrainOnceServing). A replay the startup drain routes
// through dispatchOp proves no such thing — it runs before Serve — and must not spend that signal.
func TestDispatchOp_AReplayIsNotAServedRequest(t *testing.T) {
	dd := replayProbeDaemon(t)
	status := ipc.Request{Op: ipc.OpStatus, Reply: true, Nonce: "nonce-status"}

	resp := dd.drainDispatch(context.Background(), status)
	require.True(t, resp.OK)
	select {
	case <-dd.firstServed:
		t.Fatal("a replayed request released the re-drain that waits for the first served request")
	default:
	}

	dd.dispatchOp(context.Background(), status)
	select {
	case <-dd.firstServed:
	default:
		t.Fatal("a live request must still release it")
	}
}

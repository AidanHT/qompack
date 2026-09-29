package daemon

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/contract"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/logging"
)

// mcpSeamDaemon is a daemon whose MCPInitialized seam reports seen, which declares
// mcp.server_registered as in production (InstallMCPOp binds the same seam).
//
// Deliberately NOT parallel: New declares producers into the process-wide set.
func mcpSeamDaemon(t *testing.T, seen *atomic.Bool) *daemon {
	t.Helper()
	contract.ResetProducers()
	t.Cleanup(contract.ResetProducers)
	o := NewOptions(t.TempDir(), testConfig())
	o.Log = logging.Nop()
	o.Bind(func(s *Services) {
		s.MCPInitialized = func(context.Context) bool { return seen.Load() }
	})
	d, err := New(o)
	require.NoError(t, err)
	dd, ok := d.(*daemon)
	require.True(t, ok)
	t.Cleanup(func() { _ = dd.ing.Close() })
	t.Cleanup(func() { joinReplyWork(t, dd) })
	require.True(t, contract.HasProducer(contract.CMCPRegistered), "fixture: the assertion must be live")
	return dd
}

// reportOf is the result the daemon's status page shows for id.
func reportOf(t *testing.T, dd *daemon, id contract.ID) contract.Result {
	t.Helper()
	for _, r := range dd.monitor.Report() {
		if r.ID == id {
			return r
		}
	}
	require.FailNow(t, "no result for "+string(id))
	return contract.Result{}
}

// TestSessionStart_AHandshakeThisDaemonSawCountsForTheContract: the stdio server's handshake flips
// the daemon's own MCPInitialized seam and, separately, writes history.json. A start must read the
// seam too, so a handshake whose history write was lost — overwritten by a start's own save of the
// history it loaded before the handshake landed, or refused by the disk — still counts, and a
// healthy second session does not read mcp.server_registered FAILING (Phase 4 install D4).
func TestSessionStart_AHandshakeThisDaemonSawCountsForTheContract(t *testing.T) {
	var seen atomic.Bool
	dd := mcpSeamDaemon(t, &seen)

	require.True(t, dd.dispatchOp(context.Background(),
		startRequest(dd, core.SessionID("sess-mcp-1"), "startup", "", "nonce-1")).OK)
	first := reportOf(t, dd, contract.CMCPRegistered)
	require.True(t, first.OK, "the first session's start comes before any handshake: %+v", first)

	seen.Store(true) // the handshake reached this daemon; its history write did not survive
	require.False(t, history(t, dd).MCPInitialized, "fixture: history.json does not record it")

	require.True(t, dd.dispatchOp(context.Background(),
		startRequest(dd, core.SessionID("sess-mcp-2"), "startup", "", "nonce-2")).OK)
	second := reportOf(t, dd, contract.CMCPRegistered)
	require.True(t, second.OK, "a handshake this daemon saw must satisfy the assertion: %+v", second)
	require.Equal(t, "initialize-received", second.Observed)
	require.True(t, history(t, dd).MCPInitialized, "the start records what the daemon saw")
}

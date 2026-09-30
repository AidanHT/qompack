package daemon

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/contract"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/mcp"
)

// The contract snapshot status shows is the monitor's last RunAll, which runs at SessionStart —
// before the host has connected the MCP server or written the probe into the transcript. The
// candidate 4 live re-run (UAT-01) read mcp.server_registered initialize-pending and
// hook.additional_context_delivered not-yet-observed after the session's MCP call, while the same
// store's state/history.json recorded mcp_initialized true and the sentinel observed. D50: an
// observed initialize or sentinel refreshes the snapshot, so status and doctor read what
// history.json already knows.

// mcpOpDaemon is a daemon with the production `mcp` op installed (InstallMCPOp), which binds the
// MCPInitialized seam and so declares mcp.server_registered.
//
// Deliberately NOT parallel (every caller): New declares producers into the process-wide set.
func mcpOpDaemon(t *testing.T) *daemon {
	t.Helper()
	contract.ResetProducers()
	t.Cleanup(contract.ResetProducers)
	root := t.TempDir()
	o := NewOptions(root, testConfig())
	o.Log = logging.Nop()
	require.NoError(t, InstallMCPOp(&o, mcp.ToolDeps{
		Cfg: o.Cfg, ProjectRoot: root, HostPolicy: mcpOpHostPolicy(t, root),
	}))
	d, err := New(o)
	require.NoError(t, err)
	dd, ok := d.(*daemon)
	require.True(t, ok)
	t.Cleanup(func() { _ = dd.ing.Close() })
	t.Cleanup(func() { joinReplyWork(t, dd) })
	require.True(t, contract.HasProducer(contract.CMCPRegistered), "fixture: the assertion must be live")
	return dd
}

// statusRow is the contract row the status op answers for id.
func statusRow(t *testing.T, dd *daemon, id contract.ID) contract.Result {
	t.Helper()
	resp := dd.dispatchOp(context.Background(), ipc.Request{Op: ipc.OpStatus, Reply: true, TS: core.NowMilli(dd.clk)})
	require.True(t, resp.OK, resp.Err)
	var snap StatusSnapshot
	require.NoError(t, json.Unmarshal(resp.Data, &snap))
	for _, r := range snap.Contract {
		if r.ID == id {
			return r
		}
	}
	require.FailNow(t, "the status snapshot carries no row for "+string(id))
	return contract.Result{}
}

// TestStatus_AnObservedHandshakeRefreshesTheContractSnapshot is UAT-01's mcp.server_registered row:
// the start reads the handshake pending, the stdio server then reports it, and status says it was
// received rather than the start's stale pending.
func TestStatus_AnObservedHandshakeRefreshesTheContractSnapshot(t *testing.T) {
	dd := mcpOpDaemon(t)

	require.True(t, dd.dispatchOp(context.Background(),
		startRequest(dd, "sess-snapshot-mcp", "startup", "", "nonce-start")).OK)
	require.Equal(t, "initialize-pending", statusRow(t, dd, contract.CMCPRegistered).Observed,
		"fixture: the start comes before the handshake")

	raw, err := json.Marshal(MCPOpRequest{Kind: MCPKindInitialized})
	require.NoError(t, err)
	require.True(t, dd.dispatchOp(context.Background(),
		ipc.Request{Op: ipc.OpMCP, Reply: true, Raw: raw}).OK)
	require.True(t, history(t, dd).MCPInitialized, "fixture: history.json records the handshake")

	row := statusRow(t, dd, contract.CMCPRegistered)
	require.True(t, row.OK)
	require.Equal(t, "initialize-received", row.Observed,
		"status must read the handshake history.json records, not the start's pending: %+v", row)
}

// TestStatus_AHandshakeOnlyTheDaemonSawStillRefreshes: the daemon's own seam counts a handshake
// whose history write did not survive (TestSessionStart_AHandshakeThisDaemonSawCountsForTheContract
// makes the same rule for a start), so status reads it too.
func TestStatus_AHandshakeOnlyTheDaemonSawStillRefreshes(t *testing.T) {
	var seen atomic.Bool
	dd := mcpSeamDaemon(t, &seen)

	require.True(t, dd.dispatchOp(context.Background(),
		startRequest(dd, "sess-snapshot-seam", "startup", "", "nonce-start")).OK)
	require.Equal(t, "initialize-pending", statusRow(t, dd, contract.CMCPRegistered).Observed,
		"fixture: the start comes before the handshake")

	seen.Store(true)
	require.False(t, history(t, dd).MCPInitialized, "fixture: history.json does not record it")
	require.Equal(t, "initialize-received", statusRow(t, dd, contract.CMCPRegistered).Observed)
}

// TestStatus_AnObservedSentinelRefreshesTheContractSnapshot is UAT-01's
// hook.additional_context_delivered row: the start mints the probe and reads it not yet observed, a
// prompt's scan then finds it in the transcript, and status says so.
func TestStatus_AnObservedSentinelRefreshesTheContractSnapshot(t *testing.T) {
	dd := replayProbeDaemon(t)
	const sess = core.SessionID("sess-snapshot-probe")
	transcript := filepath.Join(dd.root, "snapshot-transcript.jsonl")

	require.True(t, dd.dispatchOp(context.Background(), startRequest(dd, sess, "startup", transcript, "nonce-start")).OK)
	require.Equal(t, "not-yet-observed", statusRow(t, dd, contract.CAdditionalContext).Observed,
		"fixture: the start mints the probe it cannot have seen yet")

	h := history(t, dd)
	appendTranscript(t, transcript,
		probeLine(h.Sentinel.Token),
		`{"type":"user","message":{"role":"user","content":"hello"}}`,
	)
	promptScan(dd, sess, transcript, h.Sentinel.MintedAt+1)
	require.True(t, history(t, dd).Sentinel.Observed, "fixture: the prompt's scan finds the probe")

	row := statusRow(t, dd, contract.CAdditionalContext)
	require.True(t, row.OK)
	require.Equal(t, "sentinel-observed", row.Observed,
		"status must read the sentinel history.json records, not the start's not-yet-observed: %+v", row)
}

// TestStatus_AMissedSentinelIsNotRefreshedIntoAnObservation: a prompt whose scan misses the probe
// spends a chance and changes nothing status reports; only an observation refreshes a row.
func TestStatus_AMissedSentinelIsNotRefreshedIntoAnObservation(t *testing.T) {
	dd := replayProbeDaemon(t)
	const sess = core.SessionID("sess-snapshot-miss")
	transcript := replayTranscript(t, dd.root)

	require.True(t, dd.dispatchOp(context.Background(), startRequest(dd, sess, "startup", transcript, "nonce-start")).OK)
	h := history(t, dd)
	promptScan(dd, sess, transcript, h.Sentinel.MintedAt+1)
	require.False(t, history(t, dd).Sentinel.Observed, "fixture: the transcript carries no probe")

	require.Equal(t, "not-yet-observed", statusRow(t, dd, contract.CAdditionalContext).Observed)
}

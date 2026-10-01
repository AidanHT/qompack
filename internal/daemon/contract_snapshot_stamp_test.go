package daemon

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/contract"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/mcp"
)

// A contract row status refreshes from state/history.json is an observation the daemon recorded
// earlier: the handshake the `mcp` op saw, the prompt scan that found the probe or spent its last
// chance. Its TS is the time of THAT observation, never the time status read it, so two reads of
// unchanged state answer the same rows (D53(a); candidate 5's TestV5_ObserveToStatusRoundTrip/live
// read hook.additional_context_delivered at 1790821924508 and then at ...539 with nothing changed).

// stampReadGap is how far the clock moves between the observation and each status read: any
// non-zero step tells a read-time stamp from an observation-time one.
const stampReadGap = 5 * time.Second

// clockedProbeDaemon is replayProbeDaemon on a clock the row moves, with the production `mcp` op
// installed, so both rows status refreshes from history.json are live.
//
// Deliberately NOT parallel (every caller): New declares producers into the process-wide set.
func clockedProbeDaemon(t *testing.T, clk *fakeClock) *daemon {
	t.Helper()
	contract.ResetProducers()
	t.Cleanup(contract.ResetProducers)
	root := t.TempDir()
	o := NewOptions(root, testConfig())
	o.Log = logging.Nop()
	o.Clock = clk
	o.Bind(func(s *Services) {
		s.Rehydrate = func(context.Context, hookio.Event) (hookio.Output, error) { return hookio.Empty(), nil }
	})
	require.NoError(t, InstallMCPOp(&o, mcp.ToolDeps{
		Cfg: o.Cfg, ProjectRoot: root, HostPolicy: mcpOpHostPolicy(t, root),
	}))
	d, err := New(o)
	require.NoError(t, err)
	dd, ok := d.(*daemon)
	require.True(t, ok)
	t.Cleanup(func() { _ = dd.ing.Close() })
	t.Cleanup(func() { joinReplyWork(t, dd) })
	require.True(t, contract.HasProducer(contract.CAdditionalContext), "fixture: the probe's assertion must be live")
	require.True(t, contract.HasProducer(contract.CMCPRegistered), "fixture: the handshake's assertion must be live")
	return dd
}

// clockedSeamDaemon is mcpSeamDaemon on a clock the row moves: its MCPInitialized seam reports seen,
// and nothing records the handshake in history.json.
//
// Deliberately NOT parallel (every caller): New declares producers into the process-wide set.
func clockedSeamDaemon(t *testing.T, clk *fakeClock, seen *atomic.Bool) *daemon {
	t.Helper()
	contract.ResetProducers()
	t.Cleanup(contract.ResetProducers)
	o := NewOptions(t.TempDir(), testConfig())
	o.Log = logging.Nop()
	o.Clock = clk
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

// statusContract is the whole contract the status op answers.
func statusContract(t *testing.T, dd *daemon) []contract.Result {
	t.Helper()
	resp := dd.dispatchOp(context.Background(), ipc.Request{Op: ipc.OpStatus, Reply: true, TS: core.NowMilli(dd.clk)})
	require.True(t, resp.OK, resp.Err)
	var snap StatusSnapshot
	require.NoError(t, json.Unmarshal(resp.Data, &snap))
	return snap.Contract
}

// contractRow returns the row for id out of rows.
func contractRow(t *testing.T, rows []contract.Result, id contract.ID) contract.Result {
	t.Helper()
	for _, r := range rows {
		if r.ID == id {
			return r
		}
	}
	require.FailNow(t, "the status snapshot carries no row for "+string(id))
	return contract.Result{}
}

// TestStatus_ASpentProbeRowCarriesTheLastMissNotTheRead is candidate 5's live X01 row in the
// daemon: two prompts miss the probe, and every later status read reports the failure at the time
// the second miss was recorded, so two reads of unchanged state are equal.
func TestStatus_ASpentProbeRowCarriesTheLastMissNotTheRead(t *testing.T) {
	clk := newFakeClock(epoch)
	dd := clockedProbeDaemon(t, clk)
	const sess = core.SessionID("sess-stamp-spent")
	transcript := replayTranscript(t, dd.root)

	require.True(t, dd.dispatchOp(context.Background(), startRequest(dd, sess, "startup", transcript, "nonce-start")).OK)
	h := history(t, dd)
	clk.Advance(time.Second)
	promptScan(dd, sess, transcript, h.Sentinel.MintedAt+1)
	clk.Advance(time.Second)
	promptScan(dd, sess, transcript, h.Sentinel.MintedAt+2)
	lastMiss := core.NowMilli(clk)
	require.Equal(t, 2, history(t, dd).Sentinel.Chances, "fixture: both prompts spent a chance")

	clk.Advance(stampReadGap)
	first := statusContract(t, dd)
	clk.Advance(stampReadGap)
	second := statusContract(t, dd)

	row := contractRow(t, first, contract.CAdditionalContext)
	require.False(t, row.OK, "fixture: status reads the failure history.json records: %+v", row)
	require.Equal(t, lastMiss, row.TS,
		"the refreshed row is dated by the scan that spent the probe's last chance, not by the read")
	require.Equal(t, first, second, "two status reads of unchanged state must answer the same contract")
}

// TestStatus_AnObservedProbeRowCarriesTheScanThatFoundIt: the same for the probe's observation.
func TestStatus_AnObservedProbeRowCarriesTheScanThatFoundIt(t *testing.T) {
	clk := newFakeClock(epoch)
	dd := clockedProbeDaemon(t, clk)
	const sess = core.SessionID("sess-stamp-found")
	transcript := replayTranscript(t, dd.root)

	require.True(t, dd.dispatchOp(context.Background(), startRequest(dd, sess, "startup", transcript, "nonce-start")).OK)
	h := history(t, dd)
	appendTranscript(t, transcript, probeLine(h.Sentinel.Token))
	clk.Advance(time.Second)
	promptScan(dd, sess, transcript, h.Sentinel.MintedAt+1)
	found := core.NowMilli(clk)
	require.True(t, history(t, dd).Sentinel.Observed, "fixture: the prompt's scan finds the probe")

	clk.Advance(stampReadGap)
	first := statusContract(t, dd)
	clk.Advance(stampReadGap)
	second := statusContract(t, dd)

	row := contractRow(t, first, contract.CAdditionalContext)
	require.Equal(t, "sentinel-observed", row.Observed, "fixture: %+v", row)
	require.Equal(t, found, row.TS, "the refreshed row is dated by the scan that found the probe, not by the read")
	require.Equal(t, first, second, "two status reads of unchanged state must answer the same contract")
}

// TestStatus_AnObservedHandshakeRowCarriesTheHandshakeTime: the `mcp` op's handshake record dates
// the refreshed mcp.server_registered row.
func TestStatus_AnObservedHandshakeRowCarriesTheHandshakeTime(t *testing.T) {
	clk := newFakeClock(epoch)
	dd := clockedProbeDaemon(t, clk)

	require.True(t, dd.dispatchOp(context.Background(),
		startRequest(dd, "sess-stamp-mcp", "startup", "", "nonce-start")).OK)
	clk.Advance(time.Second)
	raw, err := json.Marshal(MCPOpRequest{Kind: MCPKindInitialized})
	require.NoError(t, err)
	require.True(t, dd.dispatchOp(context.Background(), ipc.Request{Op: ipc.OpMCP, Reply: true, Raw: raw}).OK)
	handshake := core.NowMilli(clk)

	clk.Advance(stampReadGap)
	first := statusContract(t, dd)
	clk.Advance(stampReadGap)
	second := statusContract(t, dd)

	row := contractRow(t, first, contract.CMCPRegistered)
	require.Equal(t, "initialize-received", row.Observed, "fixture: %+v", row)
	require.Equal(t, handshake, row.TS, "the refreshed row is dated by the handshake, not by the read")
	require.Equal(t, first, second, "two status reads of unchanged state must answer the same contract")
}

// TestStatus_AHandshakeOnlyTheSeamSawKeepsTheStartsTime: a handshake whose history write did not
// survive has no recorded time. The row then keeps the time of the start's evaluation it refreshes,
// which does not move between reads either.
func TestStatus_AHandshakeOnlyTheSeamSawKeepsTheStartsTime(t *testing.T) {
	var seen atomic.Bool
	clk := newFakeClock(epoch)
	dd := clockedSeamDaemon(t, clk, &seen)

	require.True(t, dd.dispatchOp(context.Background(),
		startRequest(dd, "sess-stamp-seam", "startup", "", "nonce-start")).OK)
	start := contractRow(t, statusContract(t, dd), contract.CMCPRegistered)
	require.Equal(t, "initialize-pending", start.Observed, "fixture: the start comes before the handshake")

	clk.Advance(time.Second)
	seen.Store(true)
	require.False(t, history(t, dd).MCPInitialized, "fixture: history.json does not record it")
	clk.Advance(stampReadGap)
	first := statusContract(t, dd)
	clk.Advance(stampReadGap)
	second := statusContract(t, dd)

	row := contractRow(t, first, contract.CMCPRegistered)
	require.Equal(t, "initialize-received", row.Observed)
	require.Equal(t, start.TS, row.TS, "an observation with no recorded time keeps the start's evaluation time")
	require.Equal(t, first, second, "two status reads of unchanged state must answer the same contract")
}

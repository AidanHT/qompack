package daemon

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/contract"
	"github.com/qompack/qompack/internal/core"
)

// TestSessionStart_ASecondWindowLeavesTheFirstSessionsLateRowsPending is the w13-diag review's
// finding, through the daemon's own registry and prompt route: a second session that starts while
// the first is still running must not fail the first session's MCP handshake or transcript, whether
// or not the first has had a prompt yet. Both fail only once the first session had a prompt and
// ended with neither.
//
// Deliberately NOT parallel: mcpSeamDaemon declares producers into the process-wide set.
func TestSessionStart_ASecondWindowLeavesTheFirstSessionsLateRowsPending(t *testing.T) {
	var seen atomic.Bool // the handshake never reaches this daemon
	dd := mcpSeamDaemon(t, &seen)
	ctx := context.Background()
	dir := t.TempDir()
	unwritten := filepath.Join(dir, "sess-late-1.jsonl")
	present := filepath.Join(dir, "sess-late-2.jsonl")
	require.NoError(t, os.WriteFile(present, []byte(`{"type":"user"}`+"\n"), 0o600))
	const first = core.SessionID("sess-late-1")

	start := func(sess core.SessionID, nonce string) {
		t.Helper()
		require.True(t, dd.dispatchOp(ctx, startRequest(dd, sess, "startup", present, nonce)).OK)
	}
	requireRow := func(id contract.ID, ok bool, observed, why string) {
		t.Helper()
		r := reportOf(t, dd, id)
		require.Equal(t, ok, r.OK, "%s: %+v", why, r)
		require.Equal(t, observed, r.Observed, why)
	}

	require.True(t, dd.dispatchOp(ctx, startRequest(dd, first, "startup", unwritten, "nonce-late-1")).OK)
	requireRow(contract.CTranscriptReadable, true, "transcript-pending", "the first start is too early")
	requireRow(contract.CMCPRegistered, true, "initialize-pending", "the first start is too early")

	start("sess-late-2", "nonce-late-2")
	requireRow(contract.CTranscriptReadable, true, "transcript readable", "the first session has had no prompt")
	requireRow(contract.CMCPRegistered, true, "initialize-pending", "the first session has had no prompt")

	promptScan(dd, first, unwritten, core.NowMilli(dd.clk))
	start("sess-late-3", "nonce-late-3")
	requireRow(contract.CTranscriptReadable, true, "transcript readable", "the first session is still live")
	requireRow(contract.CMCPRegistered, true, "initialize-pending", "the first session is still live")

	// The flush route's first act (handleFlush); the rest of the SessionEnd path is not under test.
	dd.registry.End(first, core.NowMilli(dd.clk))
	start("sess-late-4", "nonce-late-4")
	requireRow(contract.CTranscriptReadable, false, "an earlier session's transcript_path never appeared",
		"the first session had a prompt, ended, and its transcript never appeared")
	requireRow(contract.CMCPRegistered, false, "initialize-not-received",
		"the first session had a prompt and ended with no handshake")
}

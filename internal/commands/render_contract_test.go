package commands_test

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/commands"
	"github.com/qompack/qompack/internal/contract"
)

// The host-contract banner counts what the assertions established, not their OK column. The
// candidate 4 live re-run (plans/sdd/V6-closeout/live/report-c4.md, UAT-01) read "host contract: 9
// assertion(s), all holding" while mcp.server_registered read initialize-pending and
// hook.additional_context_delivered read not-yet-observed: an OK row that is still waiting for its
// observation was counted as a contract that holds (D50).

// contractBanner renders a daemon answer carrying results and returns the page.
func contractBanner(t *testing.T, results []contract.Result) string {
	t.Helper()
	rep := commands.CollectStatus(context.Background(), commands.StatusSources{
		Daemon: func(context.Context) (commands.DaemonStatus, time.Time, error) {
			return commands.DaemonStatus{Mode: "full", Hot: "sync", Contract: results}, collectedAt, nil
		},
	}, collectedAt)
	var out bytes.Buffer
	require.NoError(t, commands.RenderStatus(&out, rep))
	return out.String()
}

// uat01Results is the snapshot the UAT-01 status read (rerun-c4/UAT-01/cli/x4-status-json): every
// row OK, three of them still waiting for what they observe and four with nothing to judge.
func uat01Results() []contract.Result {
	return []contract.Result{
		{ID: contract.CSessionStartFires, OK: true, Observed: "first-session"},
		{ID: contract.CSessionStartSourceCompact, OK: true, Observed: "no-precompact-pending"},
		{ID: contract.CAdditionalContext, OK: true, Observed: "not-yet-observed"},
		{ID: contract.CPreCompactTiming, OK: true, Observed: "timeout-unknown"},
		{ID: contract.CPreCompactCustomInstr, OK: true, Observed: "retired"},
		{ID: contract.CHookPayloadShape, OK: true, Observed: "payload shape valid"},
		{ID: contract.CMCPRegistered, OK: true, Observed: "initialize-pending"},
		{ID: contract.CTranscriptReadable, OK: true, Observed: "transcript-pending"},
		{ID: contract.CPluginRootResolves, OK: true, Observed: "resolved"},
	}
}

// TestRenderStatus_APendingRowIsNeverCountedAsHolding is D50's banner rule on the UAT-01 snapshot:
// the banner does not say "all holding", it says how many rows are pending and names them.
func TestRenderStatus_APendingRowIsNeverCountedAsHolding(t *testing.T) {
	t.Parallel()

	text := contractBanner(t, uat01Results())
	require.NotContains(t, text, "all holding", "a pending row must not be counted as holding:\n%s", text)
	require.Contains(t, text,
		"host contract: 9 assertion(s), none failing: 2 holding, 3 pending, 4 with nothing to judge\n", text)
	require.Contains(t, text, "  pending: hook.additional_context_delivered (not-yet-observed)\n", text)
	require.Contains(t, text, "  pending: mcp.server_registered (initialize-pending)\n", text)
	require.Contains(t, text, "  pending: transcript.readable (transcript-pending)\n", text)
}

// TestRenderStatus_AFailingBannerStillCountsThePendingRows: a failure leads the banner as it always
// did, and the pending rows beside it are still counted and named rather than folded into the rest.
func TestRenderStatus_AFailingBannerStillCountsThePendingRows(t *testing.T) {
	t.Parallel()

	results := uat01Results()
	results[1] = contract.Result{
		ID: contract.CSessionStartSourceCompact, OK: false, Severity: contract.SevCritical,
		Expected: "compact", Observed: "startup",
	}
	text := contractBanner(t, results)
	require.Contains(t, text,
		"host contract: 1 of 9 assertion(s) FAILING; 2 holding, 3 pending, 3 with nothing to judge\n", text)
	require.Contains(t, text, "  session_start.source_compact (critical", text)
	require.Contains(t, text, "  pending: mcp.server_registered (initialize-pending)\n", text)
}

// TestRenderStatus_AllHoldingOnlyWhenEveryRowIsAnObservation: "all holding" remains the reading of a
// snapshot whose every row reports something actually seen.
func TestRenderStatus_AllHoldingOnlyWhenEveryRowIsAnObservation(t *testing.T) {
	t.Parallel()

	text := contractBanner(t, []contract.Result{
		{ID: contract.CAdditionalContext, OK: true, Observed: "sentinel-observed"},
		{ID: contract.CMCPRegistered, OK: true, Observed: "initialize-received"},
		{ID: contract.CTranscriptReadable, OK: true, Observed: "transcript readable"},
	})
	require.Contains(t, text, "host contract: 3 assertion(s), all holding\n", text)
	require.NotContains(t, text, "pending", text)
}

// TestRenderStatus_AnUnimplementedProducerIsNotHolding: a row whose producer this build never
// declared ran no check at all, so it has nothing to judge; it is not a contract that holds.
func TestRenderStatus_AnUnimplementedProducerIsNotHolding(t *testing.T) {
	t.Parallel()

	text := contractBanner(t, []contract.Result{
		{ID: contract.CHookPayloadShape, OK: true, Observed: "payload shape valid"},
		{ID: contract.CMCPRegistered, OK: true, Severity: contract.SevInfo, Observed: "not-yet-implemented"},
	})
	require.Contains(t, text,
		"host contract: 2 assertion(s), none failing: 1 holding, 0 pending, 1 with nothing to judge\n", text)
}

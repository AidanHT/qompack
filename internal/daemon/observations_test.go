package daemon

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/contract"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/logging"
)

// TestSessionStartRecordsCapabilityObservations pins SP-19 commit 3's daemon half: every
// SessionStart appends the monitor's nine results to state/observations.json, read as evidence per
// capability rather than as the boolean the legacy Result carries. A daemon built with no Rehydrate
// or PreCompact service leaves those producers undeclared, so their assertions must land as
// `unavailable` — the reading §12.1's OK/SevInfo/not-yet-implemented exists to stop being mistaken
// for success — and nothing may claim complete coverage.
//
// Deliberately NOT parallel: the producer set is process-wide and this test resets it, which is
// only safe while no other test in the binary is running.
func TestSessionStartRecordsCapabilityObservations(t *testing.T) {
	contract.ResetProducers()
	t.Cleanup(contract.ResetProducers)

	root := t.TempDir()
	d, err := New(Options{ProjectRoot: root, Cfg: testConfig(), Log: logging.Nop()})
	require.NoError(t, err)
	dd, ok := d.(*daemon)
	require.True(t, ok)
	t.Cleanup(func() { _ = dd.ing.Close() })

	ev := &hookio.Event{HookEventName: "SessionStart", SessionID: "sess-obs", CWD: root, Source: "startup"}
	resp := dd.dispatchOp(context.Background(), ipc.Request{Op: ipc.OpSessionStart, Session: "sess-obs", Reply: true, Event: ev})
	require.True(t, resp.OK)

	path := contract.ObservationLedgerPath(root)
	led := contract.LoadObservationLedger(path)
	require.Len(t, led.Observations, 9, "one observation per §5.19 assertion")
	require.Equal(t, hostProvider, led.Target.Provider)
	require.Empty(t, led.Target.Version, "a hook payload carries no host version; none is invented")
	require.NotEmpty(t, led.Target.Platform)
	require.NotEmpty(t, led.Target.Date)

	byID := map[contract.ID]contract.Observation{}
	for _, o := range led.Observations {
		require.Equal(t, "sess-obs", o.Scope, o.ID)
		require.NotEqual(t, "complete", o.Coverage, o.ID)
		require.NotEmpty(t, o.Capability, "every standard assertion maps to a capability: %s", o.ID)
		byID[o.ID] = o
	}
	require.Len(t, byID, 9)
	require.Equal(t, contract.OutcomeUnavailable, byID[contract.CAdditionalContext].Outcome,
		"no Rehydrate service is bound, so the producer is undeclared: unavailable, never success")
	require.Equal(t, contract.OutcomeUnavailable, byID[contract.CPreCompactCustomInstr].Outcome)
	require.NotEqual(t, contract.OutcomeObserved, byID[contract.CAdditionalContext].Outcome)

	// The ledger's own file is separate from the monitor's and the history's, and a second start
	// appends rather than overwrites.
	require.NotEqual(t, contract.HistoryPath(root), path)
	ev2 := &hookio.Event{HookEventName: "SessionStart", SessionID: "sess-obs-2", CWD: root, Source: "startup"}
	resp = dd.dispatchOp(context.Background(), ipc.Request{Op: ipc.OpSessionStart, Session: "sess-obs-2", Reply: true, Event: ev2})
	require.True(t, resp.OK)
	require.Len(t, contract.LoadObservationLedger(path).Observations, 18)
}

package daemon

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/ipc"
)

// TestSessionStartRoute_RecordsItsPhases: a slow compact SessionStart must be attributable from
// metrics/latency.json alone (C1.16 — live session 2's 10.3 s answer could not be, because only
// the whole rehydrate.build was timed). One compact start through the shipped wiring records every
// route phase and every rehydration phase once.
func TestSessionStartRoute_RecordsItsPhases(t *testing.T) {
	root := t.TempDir()
	_, dd, o := wireTestDaemon(t, root, nil)
	t.Cleanup(func() {
		if l := o.LedgerHandle(); l != nil {
			_ = l.Close()
		}
	})
	ctx := context.Background()
	_, err := dd.svc.ObservePrompt(ctx, hookio.Event{
		HookEventName: "UserPromptSubmit", SessionID: "sess-phases", CWD: root, Prompt: "keep port 8443",
	})
	require.NoError(t, err)

	ev := &hookio.Event{HookEventName: "SessionStart", SessionID: "sess-phases", CWD: root, Source: "compact"}
	resp := dd.dispatchOp(ctx, ipc.Request{Op: ipc.OpSessionStart, Session: "sess-phases", Reply: true, Event: ev})
	require.True(t, resp.OK)
	// Joins any work the route left running, with no clock: a grace that ended would cancel that work
	// before it recorded the phases this row counts (joinReplyWork). A hang is left to go test -timeout.
	dd.stopPromptRecordings(ctx)

	hists := dd.m.Snapshot().Hists
	for _, name := range []string{
		histSessionStartRoute, histSessionStartContract, histSessionStartSeam, histSessionStartFinish,
		histRehydrateLatest, histRehydrateDeps, histRehydrateSelection, histRehydrateRecord,
	} {
		require.EqualValues(t, 1, hists[name].N, "%s must be recorded once per compact start", name)
	}
}

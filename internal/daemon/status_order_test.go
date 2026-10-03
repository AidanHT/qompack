package daemon

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/ipc"
)

// The candidate 7 live lane's C4.5 (F-C7-C45-1, D53(a)): two `qompack status --json` reads of
// unchanged state, five seconds apart, listed the same two sessions in opposite orders. The status
// op built data.snapshot.sessions by ranging over the registry's map, and Go randomizes map
// iteration, so the order was a coin toss per read. The registry's snapshot is now ordered: most
// recent activity first, ties by session id.

// statusOrderReads is how many status reads a row compares. Go randomizes a map's iteration order
// per range statement, so a base that lists the five sessions below in map order disagrees with
// itself within a few reads: on candidate 7's tree this row and its registry twin went red in 10 of
// 10 runs, each by the fourth read. Thirty reads leave no realistic chance of agreeing by luck.
const statusOrderReads = 30

// statusOrderSessions starts five sessions at known times, two pairs of them tied, and returns the
// order the status snapshot must list them in.
func statusOrderSessions(t *testing.T, dd *daemon, clk *fakeClock) []core.SessionID {
	t.Helper()
	start := func(id core.SessionID) {
		resp := dd.dispatchOp(context.Background(), startRequest(dd, id, "startup", "", "nonce-"+string(id)))
		require.True(t, resp.OK, "starting %s: %s", id, resp.Err)
	}
	start("sess-order-e") // t0
	start("sess-order-c") // t0: tied with e
	clk.Advance(time.Second)
	start("sess-order-b") // t0+1s
	start("sess-order-a") // t0+1s: tied with b
	clk.Advance(time.Second)
	start("sess-order-d") // t0+2s: the most recent
	return []core.SessionID{"sess-order-d", "sess-order-a", "sess-order-b", "sess-order-c", "sess-order-e"}
}

// statusRead answers one status op and returns its whole payload and its decoded snapshot.
func statusRead(t *testing.T, dd *daemon) ([]byte, StatusSnapshot) {
	t.Helper()
	resp := dd.dispatchOp(context.Background(),
		ipc.Request{Op: ipc.OpStatus, Reply: true, TS: core.NowMilli(dd.clk)})
	require.True(t, resp.OK, resp.Err)
	var snap StatusSnapshot
	require.NoError(t, json.Unmarshal(resp.Data, &snap))
	return resp.Data, snap
}

// TestStatus_TwoReadsOfUnchangedStateListSessionsInOneOrder is C4.5's D53(a) row at the producer:
// repeated status reads of unchanged state, the clock moving between them as the lane's five
// seconds did, are byte-identical, and list the sessions most recent activity first, ties by id.
//
// Deliberately NOT parallel: New declares producers into the process-wide set.
func TestStatus_TwoReadsOfUnchangedStateListSessionsInOneOrder(t *testing.T) {
	clk := newFakeClock(epoch)
	dd := clockedProbeDaemon(t, clk)
	want := statusOrderSessions(t, dd, clk)

	first, snap := statusRead(t, dd)
	for i := 1; i < statusOrderReads; i++ {
		clk.Advance(stampReadGap)
		again, _ := statusRead(t, dd)
		require.Equal(t, string(first), string(again),
			"read %d of unchanged state disagrees with the first (D53(a))", i+1)
	}

	got := make([]core.SessionID, 0, len(snap.Sessions))
	for _, s := range snap.Sessions {
		got = append(got, s.ID)
	}
	require.Equal(t, want, got, "most recent activity first, ties by session id")
}

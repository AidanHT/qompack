package cli

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/obs"
)

// The candidate 7 live lane's C4.5 (F-C7-C45-1, D53(a)): two `qompack status --json` reads of
// unchanged state listed the same two sessions in opposite orders, because the daemon built
// data.snapshot.sessions by ranging over a map. This row is the lane's own reading, end to end: a
// daemon runDaemon composed, sessions started over the hook transport, and the status command read
// as text and as JSON, repeatedly, with nothing changing in between.

// statusOrderCLIReads is how many reads of each form the row compares. Go draws a map's iteration
// order at random per range statement, so with four sessions a base that lists them in map order
// disagrees with itself within the first few reads; 30 agreeing by chance does not happen.
const statusOrderCLIReads = 30

// startStatusOrderSession sends root's daemon a SessionStart for id, as the hook client does.
func startStatusOrderSession(t *testing.T, root string, id core.SessionID) {
	t.Helper()
	addr, err := ipc.Resolve(root)
	require.NoError(t, err)
	client := ipc.NewClientWithOptions(addr, nopSpool{}, logging.Nop(), obs.New(testClock()),
		ipc.ClientOptions{
			ProjectRoot: root, ConnectDeadline: bootstrapCallDeadline, AckDeadline: bootstrapCallDeadline,
			Clock: testClock(),
		})
	defer func() { _ = client.Close() }()
	ev := &hookio.Event{HookEventName: "SessionStart", SessionID: id, CWD: root, Source: "startup"}
	resp, err := client.Send(context.Background(), ipc.Request{
		Op: ipc.OpSessionStart, Session: id, Reply: true, Event: ev, Nonce: "nonce-" + string(id),
		TS: core.NowMilli(testClock()),
	}, bootstrapCallDeadline)
	require.NoError(t, err)
	require.True(t, resp.OK, "starting %s: %s", id, resp.Err)
}

// TestStatus_RepeatedReadsOfUnchangedStateAgree: with several sessions registered, every `status`
// and `status --json` read of unchanged state is byte-identical to the first, and the JSON lists the
// sessions in the documented order (docs/troubleshooting.md, `qompack status`).
//
// Not parallel: bootstrapDaemon resets the process-wide producer set.
func TestStatus_RepeatedReadsOfUnchangedStateAgree(t *testing.T) {
	root := bootstrapProject(t)
	stop := bootstrapDaemon(t, root)
	defer stop()

	// The daemon stamps activity by its own clock, so two starts may share a millisecond. Each start
	// here has a smaller id than the one before it, so recency and the id tie-break agree on the
	// order whichever starts tie.
	for _, id := range []core.SessionID{"sess-cli-d", "sess-cli-c", "sess-cli-b", "sess-cli-a"} {
		startStatusOrderSession(t, root, id)
	}

	read := func(args ...string) string {
		t.Helper()
		code, out, errw := fsckDispatchIn(t, root, args...)
		require.Equal(t, ExitOK, code, "stderr=%s", errw)
		return out
	}

	firstJSON := read("status", "--json")
	firstText := read("status")
	for i := 1; i < statusOrderCLIReads; i++ {
		require.Equal(t, firstJSON, read("status", "--json"),
			"status --json read %d of unchanged state disagrees with the first (D53(a))", i+1)
		require.Equal(t, firstText, read("status"),
			"status read %d of unchanged state disagrees with the first (D53(a))", i+1)
	}

	var env struct {
		Data struct {
			Primary struct {
				Source string `json:"source"`
			} `json:"primary"`
			Snapshot struct {
				Sessions []struct {
					ID core.SessionID `json:"ID"`
				} `json:"sessions"`
			} `json:"snapshot"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal([]byte(firstJSON), &env), "stdout=%s", firstJSON)
	require.Equal(t, "daemon", env.Data.Primary.Source, "fixture: the reads must be the live daemon's")
	got := make([]core.SessionID, 0, len(env.Data.Snapshot.Sessions))
	for _, s := range env.Data.Snapshot.Sessions {
		got = append(got, s.ID)
	}
	require.Equal(t, []core.SessionID{"sess-cli-a", "sess-cli-b", "sess-cli-c", "sess-cli-d"}, got,
		"most recent activity first, ties by session id")
}

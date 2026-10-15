//go:build windows

package cli

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/modwire"
	"github.com/qompack/qompack/internal/paths"
)

// TestSendModRequest_BusyWhenTheDaemonDoesNotAccept: a named pipe that exists but is not accepting
// answers the dial busy until the connect deadline. That is a daemon that exists, so the exchange
// says busy, spawns nothing, and spends at least the 250 ms floor before giving up even though the
// state's own connect deadline is the 25 ms hot-path one.
func TestSendModRequest_BusyWhenTheDaemonDoesNotAccept(t *testing.T) {
	p := newModProject(t, true)
	spawns := &modSpawns{}
	spawns.install(t)
	fakeModDaemon(t, p.root, nil)
	env := p.env("", nil)
	env.Self = "qompack-under-test"
	st := modTestState()
	require.Less(t, time.Duration(st.ConnectDeadlineMs)*time.Millisecond, hookConnectDeadlineFloor)

	began := time.Now()
	_, got := sendModRequest(context.Background(), env, p.root, st, modTestRequest(), modAnswerWait)
	elapsed := time.Since(began)

	require.False(t, got.OK)
	require.Equal(t, modwire.ReasonBusy, got.Reason)
	require.GreaterOrEqual(t, elapsed, hookConnectDeadlineFloor, "the dial is given the floor, not the state's deadline")
	require.Zero(t, spawns.count())
	require.NoDirExists(t, paths.Of(p.root).Spool)
}

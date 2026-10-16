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
	_, got := sendModRequest(context.Background(), env, p.root, st, modTestRequest(), time.Now().Add(modAnswerWait))
	elapsed := time.Since(began)

	require.False(t, got.OK)
	require.Equal(t, modwire.ReasonBusy, got.Reason)
	require.GreaterOrEqual(t, elapsed, hookConnectDeadlineFloor, "the dial is given the floor, not the state's deadline")
	require.Zero(t, spawns.count())
	require.NoDirExists(t, paths.Of(p.root).Spool)
}

// TestSendModRequest_TheDialIsBoundedByTheReplyInstant: the dial gets the 250 ms floor only while
// the reply instant leaves that much; with 30 ms left a pipe that does not accept is given up on
// well before the floor.
func TestSendModRequest_TheDialIsBoundedByTheReplyInstant(t *testing.T) {
	p := newModProject(t, true)
	spawns := &modSpawns{}
	spawns.install(t)
	fakeModDaemon(t, p.root, nil)
	env := p.env("", nil)
	env.Self = "qompack-under-test"

	began := time.Now()
	_, got := sendModRequest(context.Background(), env, p.root, modTestState(), modTestRequest(),
		began.Add(30*time.Millisecond))
	elapsed := time.Since(began)

	require.False(t, got.OK)
	require.Equal(t, modwire.ReasonBusy, got.Reason)
	require.Less(t, elapsed, hookConnectDeadlineFloor, "the reply instant, not the floor, ended the dial")
	require.Zero(t, spawns.count())
}

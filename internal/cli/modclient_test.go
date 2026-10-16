package cli

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/modwire"
	"github.com/qompack/qompack/internal/paths"
)

// modAnswerWait bounds a fake daemon's answer in the rows that expect one. It is a hang guard, not
// a measurement.
const modAnswerWait = 10 * time.Second

// modSpawns records the detached spawns a mod exchange starts, in place of the real spawner.
type modSpawns struct {
	mu    sync.Mutex
	roots []string
	selfs []string
}

// install swaps the package spawner for the recorder for the rest of the test.
func (s *modSpawns) install(t *testing.T) {
	t.Helper()
	prev := spawnDaemon
	spawnDaemon = func(root, self string) error {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.roots = append(s.roots, root)
		s.selfs = append(s.selfs, self)
		return nil
	}
	t.Cleanup(func() { spawnDaemon = prev })
}

func (s *modSpawns) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.roots)
}

// fakeModDaemon binds a real endpoint at root's resolved address. A nil handler binds without
// serving, which a named pipe answers as busy and a unix socket queues.
func fakeModDaemon(t *testing.T, root string, h ipc.Handler) {
	t.Helper()
	addr, err := ipc.Resolve(root)
	require.NoError(t, err)
	srv, err := ipc.NewServer(addr, logging.Nop(), nil, 0)
	require.NoError(t, err)
	if h == nil {
		t.Cleanup(func() { _ = srv.Close() })
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = srv.Serve(ctx, h)
	}()
	t.Cleanup(func() {
		cancel()
		_ = srv.Close()
		<-done
	})
}

// modTestState is the client state a mod exchange works under: the shipped defaults, so the
// connect deadline is the tight hot-path one the floor has to widen.
func modTestState() ipc.State { return ipc.StateFromConfig(config.Defaults()) }

// modTestRequest is a reply request on an op the daemon already routes; the exchange's
// classification is a property of the transport, not of the op.
func modTestRequest() ipc.Request {
	return ipc.Request{Op: ipc.OpStatus, Session: "sess-1", TS: 1, Reply: true}
}

func TestModConnectDeadline_HasTheHookFloor(t *testing.T) {
	for _, tc := range []struct {
		ms   uint16
		want time.Duration
	}{
		{0, hookConnectDeadlineFloor},
		{5, hookConnectDeadlineFloor},
		{25, hookConnectDeadlineFloor},
		{250, 250 * time.Millisecond},
		{400, 400 * time.Millisecond},
	} {
		st := modTestState()
		st.ConnectDeadlineMs = tc.ms
		require.Equal(t, tc.want, modConnectDeadline(st), "ConnectDeadlineMs %d", tc.ms)
	}
	require.Equal(t, 250*time.Millisecond, hookConnectDeadlineFloor, "the floor the brief names")
}

func TestModUnservedReason_MapsEverySendFailure(t *testing.T) {
	for f, want := range map[ipc.SendFailure]modwire.Reason{
		ipc.SendNotListening: modwire.ReasonCold,
		ipc.SendNotAccepted:  modwire.ReasonBusy,
		ipc.SendUnanswered:   modwire.ReasonTimeout,
		ipc.SendDisabled:     modwire.ReasonError,
		ipc.SendBroken:       modwire.ReasonError,
		"a later step":       modwire.ReasonError,
	} {
		require.Equal(t, want, modUnservedReason(f), "%q", f)
	}
}

// TestSendModRequest_Answered: a daemon that answers, whatever it says, is an answered exchange; the
// verb reads its answer.
func TestSendModRequest_Answered(t *testing.T) {
	for name, answer := range map[string]ipc.Response{
		"served":  {OK: true, Err: ""},
		"refused": {OK: false, Err: "no such verb"},
	} {
		t.Run(name, func(t *testing.T) {
			p := newModProject(t, true)
			spawns := &modSpawns{}
			spawns.install(t)
			fakeModDaemon(t, p.root, func(context.Context, ipc.Request) ipc.Response { return answer })

			env := p.env("", nil)
			env.Self = "qompack-under-test"
			resp, st := sendModRequest(context.Background(), env, p.root, modTestState(), modTestRequest(), time.Now().Add(modAnswerWait))
			require.Equal(t, modwire.Served(), st)
			require.Equal(t, answer.OK, resp.OK)
			require.Equal(t, answer.Err, resp.Err)
			require.Zero(t, spawns.count())
		})
	}
}

// TestSendModRequest_ColdStartsTheDaemonAndReturns: with nothing listening, the exchange starts the
// detached daemon through the shared spawn claim and returns cold at once, without waiting for it.
func TestSendModRequest_ColdStartsTheDaemonAndReturns(t *testing.T) {
	p := newModProject(t, true)
	spawns := &modSpawns{}
	spawns.install(t)
	env := p.env("", nil)
	env.Self = "qompack-under-test"

	_, st := sendModRequest(context.Background(), env, p.root, modTestState(), modTestRequest(), time.Now().Add(modAnswerWait))
	require.False(t, st.OK)
	require.Equal(t, modwire.ReasonCold, st.Reason)
	require.Equal(t, []string{p.root}, spawns.roots, "one detached spawn for the project")
	require.Equal(t, []string{"qompack-under-test"}, spawns.selfs)

	// The claim is the one every spawner shares, so a second cold call inside its freshness window
	// leaves the spawn already underway alone.
	_, st = sendModRequest(context.Background(), env, p.root, modTestState(), modTestRequest(), time.Now().Add(modAnswerWait))
	require.Equal(t, modwire.ReasonCold, st.Reason)
	require.Equal(t, 1, spawns.count())
	require.NoDirExists(t, paths.Of(p.root).Spool, "a mod request is never spooled")
}

// TestSendModRequest_ColdWithoutSelfSpawnsNothing: an Env with no executable path (every Env a test
// or another composition root builds) disables the spawn, as it does for the hooks.
func TestSendModRequest_ColdWithoutSelfSpawnsNothing(t *testing.T) {
	p := newModProject(t, true)
	spawns := &modSpawns{}
	spawns.install(t)

	_, st := sendModRequest(context.Background(), p.env("", nil), p.root, modTestState(), modTestRequest(), time.Now().Add(modAnswerWait))
	require.Equal(t, modwire.ReasonCold, st.Reason)
	require.Zero(t, spawns.count())
}

// TestSendModRequest_TimeoutWhenAcceptedButUnanswered: a daemon that accepts and never answers
// within the reply deadline is a timeout, and nothing is spawned or spooled.
func TestSendModRequest_TimeoutWhenAcceptedButUnanswered(t *testing.T) {
	p := newModProject(t, true)
	spawns := &modSpawns{}
	spawns.install(t)
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	fakeModDaemon(t, p.root, func(ctx context.Context, _ ipc.Request) ipc.Response {
		select {
		case <-release:
		case <-ctx.Done():
		}
		return ipc.Response{OK: true}
	})
	env := p.env("", nil)
	env.Self = "qompack-under-test"

	_, st := sendModRequest(context.Background(), env, p.root, modTestState(), modTestRequest(), time.Now().Add(50*time.Millisecond))
	require.False(t, st.OK)
	require.Equal(t, modwire.ReasonTimeout, st.Reason)
	require.Zero(t, spawns.count())
	require.NoDirExists(t, paths.Of(p.root).Spool, "a mod request is never spooled")
}

// TestSendModRequest_DisabledDaemonIsAnError: a project whose daemon is switched off is not cold, and
// nothing is dialled or spawned for it.
func TestSendModRequest_DisabledDaemonIsAnError(t *testing.T) {
	p := newModProject(t, true)
	spawns := &modSpawns{}
	spawns.install(t)
	st := modTestState()
	st.DaemonEnabled = false
	env := p.env("", nil)
	env.Self = "qompack-under-test"

	_, got := sendModRequest(context.Background(), env, p.root, st, modTestRequest(), time.Now().Add(modAnswerWait))
	require.False(t, got.OK)
	require.Equal(t, modwire.ReasonError, got.Reason)
	require.NotEmpty(t, got.Detail)
	require.Zero(t, spawns.count())
	require.NoDirExists(t, paths.Of(p.root).Spool)
}

// TestSendModRequest_NoTimeLeftDialsNothing: a reply instant already past leaves nothing to wait
// for, so the exchange is a timeout without a dial; a dial would have found nothing listening and
// reported cold, and started a daemon.
func TestSendModRequest_NoTimeLeftDialsNothing(t *testing.T) {
	p := newModProject(t, true)
	spawns := &modSpawns{}
	spawns.install(t)
	env := p.env("", nil)
	env.Self = "qompack-under-test"

	_, st := sendModRequest(context.Background(), env, p.root, modTestState(), modTestRequest(),
		time.Now().Add(-time.Millisecond))
	require.False(t, st.OK)
	require.Equal(t, modwire.ReasonTimeout, st.Reason)
	require.Zero(t, spawns.count(), "nothing was dialled, so nothing was found cold")
}

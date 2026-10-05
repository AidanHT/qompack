package daemon

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/contract"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/logging"
)

// The V6 close-out's NOUP row (w5-coldstart, D17): under CPU co-load one daemon took daemon.lock,
// listened and wrote state.bin, and then could not be dialled for 30 s. It was not gone: it was
// still in Run's startup, replaying the spool, and Run started its accept loop only after that. A
// named pipe with no accept pending refuses every dial on Windows (the dial waits for an instance
// and times out), so for the whole of the startup the daemon that held the project looked absent:
// session-start's poll missed it, the hook's own connect failed and spooled, and each lazy spawn
// that followed started another daemon that lost the lock and exited. On POSIX the kernel's listen
// backlog queued the same dials, so there the daemon was merely slow to answer.

// startupDialBound is how long TestRun_AcceptsDialsWhileItsStartupDrainRuns gives one dial into a
// daemon whose startup drain is held open. A dial into an accepting endpoint completes at once; the
// bound only has to outlast a loaded machine, and a refusing endpoint fails it however long it is.
const startupDialBound = time.Second

// startupReplyBound is how long that row's live request may wait for its ACK: the startup drain is
// released right after the dial, so the ACK follows as soon as the replay and the rest of the
// startup are done.
const startupReplyBound = 10 * time.Second

// startupHoldCheck is how long that row watches the live request NOT being served while the replay
// is held: long enough for the request to have reached the daemon, which can then only hold it.
const startupHoldCheck = 300 * time.Millisecond

// TestRun_AcceptsDialsWhileItsStartupDrainRuns: from the moment Run publishes its endpoint it takes
// dials, and a request that arrives while the startup drain is still replaying waits for the replay
// and is then served — after it, never ahead of it.
func TestRun_AcceptsDialsWhileItsStartupDrainRuns(t *testing.T) {
	root := t.TempDir()
	t.Setenv("QOMPACK_IPC_ADDR", uniqueTestAddr(t))

	const replayedSess = core.SessionID("sess-spooled-before-start")
	const liveSess = core.SessionID("sess-live-during-start")
	writeClientSpoolLine(t, root, "client-66666.ndjson", spooledObserveTool(root, replayedSess))

	replaying := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseDrain := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(releaseDrain)

	var mu sync.Mutex
	var observed []core.SessionID
	liveSeen := make(chan struct{})
	var o Options
	o.ProjectRoot = root
	o.Cfg = runTestConfig()
	o.Log = logging.Nop()
	o.Clock = core.SystemClock()
	o.Bind(func(s *Services) {
		s.ObserveTool = func(_ context.Context, e hookio.Event) error {
			mu.Lock()
			observed = append(observed, e.SessionID)
			mu.Unlock()
			switch {
			case e.SessionID == replayedSess: // only the spool carries it: the drain is replaying
				close(replaying)
				<-release // the startup drain stays inside its replay until the test lets it go
			case e.SessionID == liveSess:
				close(liveSeen)
			}
			return nil
		}
	})
	d, err := New(o)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan struct{})
	go func() {
		defer close(runDone)
		_ = d.Run(ctx)
	}()
	// However the test ends, Run is stopped and waited for before TempDir removes the project under
	// it (cleanups run last-registered first).
	t.Cleanup(func() {
		cancel()
		releaseDrain()
		select {
		case <-runDone:
		case <-hangGuard(t):
			t.Error("Run did not shut down after cancellation")
		}
	})

	select {
	case <-replaying:
	case <-hangGuard(t):
		t.Fatal("Run's startup drain never replayed the spooled line")
	}

	// Run listens before its startup drain, so the endpoint exists now. It must take the dial.
	addr, err := ipc.Resolve(root)
	require.NoError(t, err)
	require.True(t, ipc.Probe(addr, startupDialBound),
		"a daemon still replaying its spool must take a dial, not look absent until its startup ends")

	// A live event sent now is held until the startup is done, then served after the replay.
	c := ipc.NewClientWithOptions(addr, nil, logging.Nop(), nil, ipc.ClientOptions{
		State:           ipc.State{Mode: contract.ModeFull, DaemonEnabled: true},
		ConnectDeadline: startupDialBound,
		AckDeadline:     startupReplyBound,
	})
	defer func() { _ = c.Close() }()
	nonce, err := ipc.NewDeliveryNonce()
	require.NoError(t, err)
	live := spooledObserveTool(root, liveSess)
	live.Nonce = nonce
	sent := make(chan ipc.Response, 1)
	go func() {
		resp, _ := c.Send(context.Background(), live, startupReplyBound)
		sent <- resp
	}()
	select {
	case <-liveSeen:
		t.Fatal("a live event was dispatched while the startup drain was still replaying")
	case resp := <-sent:
		t.Fatalf("a live event was answered while the startup drain was still replaying: %+v", resp)
	case <-time.After(startupHoldCheck):
	}
	releaseDrain()

	select {
	case resp := <-sent:
		require.True(t, resp.OK, "the event sent during the startup drain must be accepted once the startup is done")
	case <-time.After(startupReplyBound + time.Second):
		t.Fatal("the event sent during the startup drain was never answered")
	}
	select {
	case <-liveSeen:
	case <-hangGuard(t):
		t.Fatal("the accepted live event never reached the observer")
	}
	mu.Lock()
	got := append([]core.SessionID(nil), observed...)
	mu.Unlock()
	require.Equal(t, []core.SessionID{replayedSess, liveSess}, got,
		"the spool replay comes first; a live event is never dispatched ahead of it")
}

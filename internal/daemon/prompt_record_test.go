package daemon

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/logging"
)

// promptRecordWait bounds how long a test waits on a channel the recording goroutine owns. It is a
// hang guard only, never a budget under test: every assertion below is ordered by channels.
const promptRecordWait = 10 * time.Second

// heldPromptSeam is an ObservePrompt seam that stands in for observer.OnUserPrompt held past the
// reply deadline — the session lock taken by ingest workers draining a tool backlog, or a slow disk.
// It signals entry, blocks until released, and then reports ctx.Err(): the exact check
// store.PutBytes and store.RecordToolUse make first, whose failure observer.onUserPrompt
// soft-drops into observer.err.prompt.put.
type heldPromptSeam struct {
	entered  chan struct{}
	release  chan struct{}
	recorded chan error

	mu    sync.Mutex
	calls int
}

func newHeldPromptSeam() *heldPromptSeam {
	return &heldPromptSeam{
		entered:  make(chan struct{}, 1),
		release:  make(chan struct{}),
		recorded: make(chan error, 1),
	}
}

func (s *heldPromptSeam) observe(ctx context.Context, _ hookio.Event) (hookio.Output, error) {
	s.mu.Lock()
	s.calls++
	s.mu.Unlock()
	s.entered <- struct{}{}
	select {
	case <-s.release:
	case <-ctx.Done():
	}
	s.recorded <- ctx.Err()
	return hookio.Output{HookSpecificOutput: &hookio.HSO{
		HookEventName: "UserPromptSubmit", AdditionalContext: "late thrash warning",
	}}, nil
}

func (s *heldPromptSeam) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func newPromptSeamDaemon(t *testing.T, seam *heldPromptSeam) *daemon {
	t.Helper()
	o := Options{ProjectRoot: t.TempDir(), Cfg: testConfig(), Log: logging.Nop()}
	o.Bind(func(s *Services) { s.ObservePrompt = seam.observe })
	d, err := New(o)
	require.NoError(t, err)
	dd, ok := d.(*daemon)
	require.True(t, ok)
	return dd
}

func promptRequest(dd *daemon, sess core.SessionID) ipc.Request {
	ev := &hookio.Event{HookEventName: "UserPromptSubmit", SessionID: sess, Prompt: "fix the refresh handler"}
	return ipc.Request{Op: ipc.OpObservePrompt, Session: sess, Reply: true, Event: ev, TS: core.NowMilli(dd.clk)}
}

// TestObservePrompt_RecordingOutlivesTheReplyDeadline is the G2.3 regression behind the V5
// close-out's lost first prompt: promptReplyDeadline bounds how long the HOOK waits for its reply,
// and must never cancel the verbatim capture itself. The seam is held past the deadline and then
// released; the recording must still see a live context, and its late Output must not reach the
// reply that already went out empty.
func TestObservePrompt_RecordingOutlivesTheReplyDeadline(t *testing.T) {
	t.Parallel()

	seam := newHeldPromptSeam()
	dd := newPromptSeamDaemon(t, seam)
	t.Cleanup(func() { _ = dd.Stop(context.Background()) })

	req := promptRequest(dd, "sess-late")
	resp := dd.dispatchOp(context.Background(), req)

	// The reply went out on the deadline, empty: a prompt is never blocked on the daemon.
	require.True(t, resp.OK)
	require.NotNil(t, resp.Output)
	require.Nil(t, resp.Output.HookSpecificOutput, "an overrun reply must still be hookio.Empty()")

	select {
	case <-seam.entered:
	case <-time.After(promptRecordWait):
		require.FailNow(t, "the ObservePrompt seam was never called")
	}
	// Released only now, after dispatchOp has returned and every deferred cancel on the reply path
	// has run — the instant the old code's recording context was already dead.
	close(seam.release)

	select {
	case err := <-seam.recorded:
		require.NoError(t, err, "the verbatim capture must not run under the reply deadline's context")
	case <-time.After(promptRecordWait):
		require.FailNow(t, "the recording never finished")
	}
	require.Equal(t, 1, seam.callCount())
	require.Equal(t, int64(1), dd.m.Counter(counterPromptReplyLate).Value(),
		"a reply that went out before its capture finished must be counted")

	// No double recording: the WAL line this route appended replays through runIngested — the
	// worker pool's job and the drain's dispatch alike — which must never call the seam again.
	require.True(t, dd.runIngested(context.Background(), req).OK)
	require.True(t, dd.drainDispatch(context.Background(), req).OK)
	require.Equal(t, 1, seam.callCount(), "a replayed observe.prompt must not record the prompt twice")
}

// TestObservePrompt_StopJoinsAnInFlightRecording: a capture still running when Stop begins is given
// the drain's window to finish on its own, with its context intact, and Stop does not return until
// it has — no goroutine outlives the daemon, and nothing it writes races the store's close.
func TestObservePrompt_StopJoinsAnInFlightRecording(t *testing.T) {
	t.Parallel()

	seam := newHeldPromptSeam()
	dd := newPromptSeamDaemon(t, seam)
	// The post-Stop dispatch below still WAL-appends (the route's Accept runs before the capture
	// gate), reopening a handle Stop's own ingest close has already passed.
	t.Cleanup(func() { _ = dd.ing.Close() })

	resp := dd.dispatchOp(context.Background(), promptRequest(dd, "sess-stop-join"))
	require.True(t, resp.OK)
	<-seam.entered

	stopped := make(chan error, 1)
	go func() { stopped <- dd.Stop(context.Background()) }()

	// Release the capture only once Stop has closed the gate — i.e. is inside the join.
	require.Eventually(t, func() bool {
		dd.promptMu.Lock()
		defer dd.promptMu.Unlock()
		return dd.promptClosed
	}, promptRecordWait, time.Millisecond)
	close(seam.release)

	select {
	case <-stopped:
	case <-time.After(promptRecordWait):
		require.FailNow(t, "Stop never returned")
	}
	// Buffered, and sent before the seam returned: present now only if Stop waited for it.
	select {
	case err := <-seam.recorded:
		require.NoError(t, err, "a capture that finishes inside the grace window must not be cancelled")
	default:
		require.FailNow(t, "Stop returned before the in-flight capture had finished")
	}

	// The gate stays shut: a prompt arriving after Stop starts no capture that could outlive it.
	resp = dd.dispatchOp(context.Background(), promptRequest(dd, "sess-after-stop"))
	require.True(t, resp.OK)
	require.NotNil(t, resp.Output)
	require.Nil(t, resp.Output.HookSpecificOutput)
	require.Equal(t, 1, seam.callCount(), "no capture may start once Stop has joined the captures")
}

// TestObservePrompt_StopCancelsARecordingThatOutlivesItsGrace: shutdown still governs the capture.
// Stop is handed a context that is already done, so the grace window is already spent; the capture
// that is still held must then see its context cancelled, and Stop must still join it.
func TestObservePrompt_StopCancelsARecordingThatOutlivesItsGrace(t *testing.T) {
	t.Parallel()

	seam := newHeldPromptSeam()
	dd := newPromptSeamDaemon(t, seam)

	resp := dd.dispatchOp(context.Background(), promptRequest(dd, "sess-stop-cancel"))
	require.True(t, resp.OK)
	<-seam.entered

	spent, cancel := context.WithCancel(context.Background())
	cancel()
	require.NoError(t, dd.Stop(spent))

	select {
	case err := <-seam.recorded:
		require.ErrorIs(t, err, context.Canceled, "past its grace, a capture is cancelled by shutdown")
	default:
		require.FailNow(t, "Stop returned without joining the cancelled capture")
	}
}

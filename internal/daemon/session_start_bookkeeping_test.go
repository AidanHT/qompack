package daemon

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/ipc"
)

// The compact SessionStart route answers without waiting for the observer's SessionStart
// bookkeeping (frontier adoption, the segment it ensures), which runs detached behind the session's
// lock (session_start_compact.go). Before, that bookkeeping finished before the answer, so the
// host's next hook for the session always found it done. These rows pin that it still does: every
// observer seam that works on the session's state waits for the session's pending bookkeeping, and
// no other session's does.

// bookkeepingFixture is a daemon whose Rehydrate answers at once and whose observer SessionStart —
// the bookkeeping — blocks until released, with every per-session observer seam recording whether
// the bookkeeping had finished when it was entered.
type bookkeepingFixture struct {
	dd      *daemon
	entered chan struct{}
	release chan struct{}

	mu         sync.Mutex
	kept       bool
	calls      map[string]bool // seam and session -> whether the bookkeeping had finished
	called     chan string
	enteredOne sync.Once
}

func newBookkeepingFixture(t *testing.T) *bookkeepingFixture {
	t.Helper()
	f := &bookkeepingFixture{
		entered: make(chan struct{}), release: make(chan struct{}),
		calls: map[string]bool{}, called: make(chan string, 16),
	}
	record := func(seam string, sess core.SessionID) {
		f.mu.Lock()
		f.calls[seam+" "+string(sess)] = f.kept
		f.mu.Unlock()
		f.called <- seam + " " + string(sess)
	}
	o := NewOptions(t.TempDir(), testConfig())
	o.Bind(func(s *Services) {
		s.Rehydrate = func(context.Context, hookio.Event) (hookio.Output, error) {
			return hookio.SessionStartOutput("<!-- qompack:injected seq=1 ver=1 -->ready<!-- /qompack:injected -->"), nil
		}
		s.SessionStart = func(ctx context.Context, _ hookio.Event) (hookio.Output, error) {
			f.enteredOne.Do(func() { close(f.entered) })
			<-f.release
			f.mu.Lock()
			f.kept = true
			f.mu.Unlock()
			return hookio.Empty(), nil
		}
		s.ObserveTool = func(_ context.Context, e hookio.Event) error { record("tool", e.SessionID); return nil }
		s.ObserveStop = func(_ context.Context, e hookio.Event, _ bool) error { record("stop", e.SessionID); return nil }
		s.ObservePrompt = func(_ context.Context, e hookio.Event) (hookio.Output, error) {
			record("prompt", e.SessionID)
			return hookio.Empty(), nil
		}
		s.SessionEnd = func(_ context.Context, e hookio.Event) error { record("end", e.SessionID); return nil }
	})
	d, err := New(o)
	require.NoError(t, err)
	dd, ok := d.(*daemon)
	require.True(t, ok)
	f.dd = dd
	t.Cleanup(func() { _ = dd.ing.Close() })
	t.Cleanup(func() {
		f.open()
		joinReplyWork(t, dd)
	})
	return f
}

func (f *bookkeepingFixture) open() {
	select {
	case <-f.release:
	default:
		close(f.release)
	}
}

// keptWhenCalled reports whether seam had been entered for sess, and whether the bookkeeping had
// finished by then.
func (f *bookkeepingFixture) keptWhenCalled(seam string, sess core.SessionID) (kept, called bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	kept, called = f.calls[seam+" "+string(sess)]
	return kept, called
}

// hookRequest is a hot-path request for sess, as an ingest worker or a drain replay hands it to
// runIngested.
func hookRequest(op ipc.Op, sess core.SessionID) ipc.Request {
	return ipc.Request{Op: op, Session: sess, Event: &hookio.Event{SessionID: sess, Prompt: "next"}}
}

// TestSessionStartCompact_SessionsNextEventWaitsForTheBookkeeping: the compact answer arrives while
// the observer's bookkeeping is still held; the same session's next tool result, Stop, prompt
// capture and SessionEnd then reach the observer only after that bookkeeping has finished, while
// another session's work is not held up by it at all.
func TestSessionStartCompact_SessionsNextEventWaitsForTheBookkeeping(t *testing.T) {
	f := newBookkeepingFixture(t)
	const sess, other = core.SessionID("sess-kept"), core.SessionID("sess-other")

	resp, _ := dispatchWithin(t, f.dd, compactRequest(f.dd.root, sess), compactTestBound,
		"the compact answer must not wait for the bookkeeping")
	require.Contains(t, additionalContext(resp.Output), "ready", "the rehydration is the answer")
	select {
	case <-f.entered:
	case <-time.After(compactTestBound):
		t.Fatal("the observer's SessionStart bookkeeping never started")
	}

	ctx := context.Background()
	var wg sync.WaitGroup
	for _, op := range []ipc.Op{ipc.OpObserveTool, ipc.OpObserveStop, ipc.OpObservePrompt} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			f.dd.runIngested(ctx, hookRequest(op, sess))
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = f.dd.svc.SessionEnd(ctx, hookio.Event{SessionID: sess})
	}()

	// Another session's work goes straight through while this session's bookkeeping is held.
	f.dd.runIngested(ctx, hookRequest(ipc.OpObserveTool, other))
	kept, called := f.keptWhenCalled("tool", other)
	require.True(t, called)
	require.False(t, kept, "another session's work did not wait for this session's bookkeeping")

	// Give the held session's work every chance to overtake the bookkeeping, then release it. The
	// wait is for the failure to show, not for the fix: nothing it waits for arrives when the
	// ordering holds.
	overtook := time.NewTimer(300 * time.Millisecond)
	defer overtook.Stop()
	for waiting := true; waiting; {
		select {
		case name := <-f.called:
			if strings.HasSuffix(name, " "+string(sess)) {
				t.Errorf("%s reached the observer before the session's compact bookkeeping finished", name)
			}
		case <-overtook.C:
			waiting = false
		}
	}
	f.open()
	wg.Wait()

	for _, seam := range []string{"tool", "stop", "prompt", "end"} {
		kept, called := f.keptWhenCalled(seam, sess)
		require.True(t, called, "%s never reached the observer", seam)
		require.True(t, kept, "%s reached the observer before the session's compact bookkeeping finished", seam)
	}
}

// TestSessionStartCompact_BookkeepingGateHonoursCancellation: a caller whose context ends while the
// session's bookkeeping is still held is not kept waiting for it — Stop cancels the workers'
// context, and a drain's bounded context expires.
func TestSessionStartCompact_BookkeepingGateHonoursCancellation(t *testing.T) {
	f := newBookkeepingFixture(t)
	const sess = core.SessionID("sess-cancel")
	dispatchWithin(t, f.dd, compactRequest(f.dd.root, sess), compactTestBound, "no answer")
	<-f.entered

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = f.dd.svc.SessionEnd(ctx, hookio.Event{SessionID: sess})
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(compactTestBound):
		t.Fatal("a cancelled caller was kept waiting on the session's bookkeeping")
	}
}

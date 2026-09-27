package daemon

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/store"
)

// V6 close-out w6-gcserial. A session end runs a store GC pass (observer.OnSessionEnd, step 6), the
// ends run on goroutines of their own since C1.15, and the idle scheduler runs one too (gcTask). The
// store now runs one pass at a time; this file checks it through the daemon's own two triggers.

// sessionEndGCProbe counts the store GC passes that are past their harvest at once, through the
// store's after-harvest hook, and holds the first one there until open is called.
type sessionEndGCProbe struct {
	inside, most, entries atomic.Int32
	first                 chan struct{}
	release               chan struct{}
	once                  sync.Once
}

func (p *sessionEndGCProbe) open() { p.once.Do(func() { close(p.release) }) }

// installSessionEndGCProbe installs the probe; cleanup releases the held pass and removes the hook.
// The hook is process-global, so the test that installs it must not run in parallel.
func installSessionEndGCProbe(t *testing.T) *sessionEndGCProbe {
	t.Helper()
	p := &sessionEndGCProbe{first: make(chan struct{}), release: make(chan struct{})}
	restore := store.SetGCAfterHarvestHookForTest(func() {
		n := p.inside.Add(1)
		for {
			m := p.most.Load()
			if n <= m || p.most.CompareAndSwap(m, n) {
				break
			}
		}
		if p.entries.Add(1) == 1 {
			close(p.first)
			<-p.release
		}
		p.inside.Add(-1)
	})
	t.Cleanup(func() {
		p.open()
		restore()
	})
	return p
}

// TestSessionEnd_GCPassesQueueBehindTheIdleSchedulersPass: while the idle scheduler's GC pass is in
// the middle of its work, two sessions end. Neither end's own pass may start until the idle pass is
// over; both wait behind it, and one follow-up pass that starts after it answers both. Before the fix
// both ends' passes ran beside the idle one (runs/02).
func TestSessionEnd_GCPassesQueueBehindTheIdleSchedulersPass(t *testing.T) {
	// Not parallel: the store's after-harvest hook is process-global.
	dd, hold, root := flushAsyncDaemon(t)
	hold.open() // the ends run straight through; this test holds the GC pass instead
	probe := installSessionEndGCProbe(t)

	idle := &schedRuntime{cfg: testConfig(), log: logging.Nop(), st: dd.svc.Store}
	idleDone := make(chan error, 1)
	go func() { idleDone <- idle.gcTask(context.Background()) }()
	select {
	case <-probe.first:
	case <-time.After(liveOrderBound):
		require.FailNow(t, "the idle scheduler's GC pass never reached its harvest hook")
	}

	for i, sess := range []core.SessionID{"sess-gc-serial-a", "sess-gc-serial-b"} {
		resp := flushAsyncDispatch(t, dd, flushAsyncRequest(dd, root, sess, orderNonce(60+i)))
		require.True(t, resp.OK, "a durably accepted flush is acknowledged: %q", resp.Err)
	}

	// Either an end's pass reaches the hook beside the held idle pass, or both ends are counted as
	// waiting behind it. Nothing else ends this wait.
	queued := dd.m.Counter(store.CounterGCQueued)
	require.Eventually(t, func() bool {
		return probe.most.Load() > 1 || queued.Value() == 2
	}, liveOrderBound, liveOrderTick, "neither session end's GC overlapped the idle pass nor queued behind it")
	require.EqualValues(t, 1, probe.most.Load(),
		"a session end's GC pass ran while the idle scheduler's pass was still in the middle of its work")

	probe.open()
	select {
	case err := <-idleDone:
		require.NoError(t, err)
	case <-time.After(liveOrderBound):
		require.FailNow(t, "the idle scheduler's GC task never returned")
	}
	flushAsyncAwait(t, dd)
	require.EqualValues(t, 2, hold.calls.Load(), "both sessions ended")
	require.EqualValues(t, 1, probe.most.Load(), "no two passes ever overlapped")
	require.EqualValues(t, 2, probe.entries.Load(),
		"the idle pass, then exactly one follow-up pass for both session ends that queued behind it")
}

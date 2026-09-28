package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/paths"
)

// sp05d1IdleBudget is the wall-clock budget the idle tick hands RunOnce in this scenario. It is
// deliberately generous: RunOnce arms a REAL timer for it, and a small value would race the
// lease-journal open, capture fsync and first-line dispatch that a fresh TempDir puts in front of
// the second line (a 20 ms budget failed 16 of 40 co-loaded runs). The budget's expiry is instead
// driven by sp05d1BudgetContext, so this timer must never be the one that fires.
const sp05d1IdleBudget = time.Hour

// sp05d1BudgetContext is the parent context the idle tick runs under, with the budget's expiry
// under the test's control: expire closes Done with Err() == context.DeadlineExceeded, which is
// exactly what RunOnce's own timer would have produced, and the timeout context RunOnce derives
// from it inherits that error down to the per-line handler context. The interrupted handler
// expires it from inside its binding, so "the budget ran out mid-line" is a program order, not a
// wall-clock coincidence.
type sp05d1BudgetContext struct {
	context.Context
	done chan struct{}
	once sync.Once
}

func newSP05D1BudgetContext() *sp05d1BudgetContext {
	return &sp05d1BudgetContext{Context: context.Background(), done: make(chan struct{})}
}

func (c *sp05d1BudgetContext) Done() <-chan struct{} { return c.done }

func (c *sp05d1BudgetContext) Err() error {
	select {
	case <-c.done:
		return context.DeadlineExceeded
	default:
		return nil
	}
}

// expire ends the budget the way its deadline would have.
func (c *sp05d1BudgetContext) expire() { c.once.Do(func() { close(c.done) }) }

// sp05d1Line encodes one complete spool record for the SP05-D1 scenario, distinguished by ts so a
// dispatch counter can tell the acknowledged line from the interrupted one.
func sp05d1Line(t *testing.T, ts core.UnixMilli) []byte {
	t.Helper()
	line, err := ipc.EncodeRequest(ipc.Request{Op: ipc.OpObserveTool, Session: "sess-sp05d1", TS: ts})
	require.NoError(t, err)
	return line
}

// sp05d1PersistedState reads state/drain.json back the way a restarted process would — from the
// bytes, not from any drainer's memory.
func sp05d1PersistedState(t *testing.T, root string) drainState {
	t.Helper()
	b, err := os.ReadFile(drainStatePath(root))
	require.NoError(t, err, "the interrupted pass must have persisted its progress")
	var st drainState
	require.NoError(t, json.Unmarshal(b, &st))
	return st
}

// sp05d1SeenCommitted reports whether the shared seen-set records key as a completed delivery.
func sp05d1SeenCommitted(s *seenSet, key core.Hash) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.set[key]
	return ok
}

// TestCarriedDefect_SP05D1_IdleBudgetExpiryLeavesInterruptedLinePending is SP05-D1's exact
// scenario: the idle tick registers the drain under a budget, the budget expires while the drain
// is mid-line — the handler is still binding when its context dies, and answers the way
// runIngested does ("observation handling interrupted") — and then the daemon either retries in
// the same process or restarts.
//
// The defect was that the line was consumed on BOTH ledgers before the binding completed: the
// spool offset had already advanced past it and the shared seen-set already said it ran, so
// neither the same process nor a restart ever dispatched it again. The two recovery cases below
// are the two ledgers: a restart reads only the persisted offset, and a same-process retry is the
// one the in-memory seen-set can wrongly suppress.
func TestCarriedDefect_SP05D1_IdleBudgetExpiryLeavesInterruptedLinePending(t *testing.T) {
	cases := []struct {
		name    string
		restart bool
	}{
		{name: "restart redelivers the interrupted line", restart: true},
		{name: "same process retries the interrupted line", restart: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			acked := sp05d1Line(t, core.UnixMilli(1))
			interrupted := sp05d1Line(t, core.UnixMilli(2))
			p := filepath.Join(paths.Of(root).Spool, "client-sp05d1.ndjson")
			require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o700))
			require.NoError(t, os.WriteFile(p, append(append([]byte(nil), acked...), interrupted...), 0o600))
			base := filepath.Base(p)

			// The dispatcher acknowledges the first line and, while the budget is live, lets the
			// budget expire from inside the second line's binding and holds that binding until
			// its own context dies — that is the interrupted binding. Once the budget has
			// expired, every later dispatch is a healthy handler.
			budget := newSP05D1BudgetContext()
			var starve atomic.Bool
			starve.Store(true)
			var calls, retried atomic.Int64
			dispatch := func(ctx context.Context, req ipc.Request) ipc.Response {
				calls.Add(1)
				if req.TS == 1 || !starve.Load() {
					if req.TS == 2 {
						retried.Add(1)
					}
					return ipc.Response{OK: true}
				}
				budget.expire()
				<-ctx.Done()
				return ipc.Response{Err: "observation handling interrupted"}
			}

			clk := newFakeClock(epoch)
			seen := newSeenSet(8)
			dr := newDrainer(DrainConfig{Root: root, Clock: clk, Dispatch: dispatch, Seen: seen})

			var drainErr error
			idle := newIdleController(1, clk, nil, nil, nil)
			idle.Register(idleTaskDrain, idlePrioDrain, func(ctx context.Context) error {
				_, drainErr = dr.Drain(ctx)
				return drainErr
			})
			ran, err := idle.RunOnce(budget, sp05d1IdleBudget)
			require.NoError(t, err)
			require.Equal(t, []string{idleTaskDrain}, ran)
			require.ErrorIs(t, drainErr, context.DeadlineExceeded,
				"the drain must report its own death as the budget's deadline, not as a handler refusal")
			require.Equal(t, int64(2), calls.Load(), "both lines were dispatched before the budget expired")
			starve.Store(false)

			// Ledger 1: the persisted offset must point AT the interrupted line, not past it.
			fs := sp05d1PersistedState(t, root)[base]
			require.NotNil(t, fs, "the acknowledged first line must have been persisted")
			require.Equal(t, int64(len(acked)), fs.Offset,
				"an interrupted binding must not advance the offset past its line")
			require.False(t, fs.Done)
			require.FileExists(t, p)

			// Ledger 2: the shared seen-set must not say the interrupted line ran.
			key := core.HashBytes(walHashDomain, interrupted[:len(interrupted)-1])
			require.False(t, sp05d1SeenCommitted(seen, key),
				"an interrupted binding must not be committed to the seen-set")

			recovering := dr
			if tc.restart {
				recovering = newDrainer(DrainConfig{Root: root, Clock: clk, Dispatch: dispatch, Seen: newSeenSet(8)})
			}
			n, err := recovering.Drain(context.Background())
			require.NoError(t, err)
			require.Equal(t, 1, n, "exactly the interrupted line is redelivered")
			require.Equal(t, int64(1), retried.Load(), "the redelivered request is the interrupted one")
			require.Equal(t, int64(3), calls.Load(), "the acknowledged first line is not dispatched again")
			require.NoFileExists(t, p, "the file is released once every line is acknowledged")
		})
	}
}

// TestIdleDrain_ALineSlowerThanTheIdleBudgetIsPublishedAndDoesNotStrandTheRest: RunOnce hands each
// idle task what is left of idleRunBudget, and gave the drain task that as a context deadline, which
// cancelled the line the pass was publishing. A spooled delivery whose publication took longer than
// the budget — a capture on a host with a deep fsync queue — was cancelled by every idle pass, and
// every spool after it in pass order was never reached: under -race with CPU and fsync co-load on
// Linux the flush's daemon in TestV3_LiveSessionWriteSetAndAppendOnly logged "ObservePrompt capture
// not durable" and "idle task returned an error name=drain err=context deadline exceeded" at every
// idle tick for minutes, with the session's SessionEnd flush stranded in a client spool behind it
// and no "observer: gc" line in 300 s (runs/linux-diag, the x09 diagnostic's third iteration). The
// idle drain must give the line it started its own drainLineDeadline, as a requested or watcher pass
// does (withPassBudget), so the slow line publishes and the next pass reaches the spool behind it.
// The row counts the attempts cancelled inside the slow part, which the idle budget did to every
// attempt and a line's own deadline cannot.
func TestIdleDrain_ALineSlowerThanTheIdleBudgetIsPublishedAndDoesNotStrandTheRest(t *testing.T) {
	dd, _, root := laneTestDaemon(t)
	slowReq := liveOrderTool(dd, root, "sess-idle-slow", 1)
	behind := liveOrderTool(dd, root, "sess-idle-behind", 2)
	slow := idleRunBudget + time.Second // longer than the idle budget, inside drainLineDeadline
	require.Less(t, slow, drainLineDeadline, "fixture: the slow line must fit its own deadline")
	counts := spoolWatchSlowDrain(dd, map[string]bool{slowReq.Nonce: true}, slow)
	// Pass order is lexical: the slow spool comes first, the other waits behind it.
	writeSpoolLines(t, root, "client-8401.ndjson", slowReq)
	writeSpoolLines(t, root, "client-8402.ndjson", behind)

	ctx := context.Background()
	deadline := time.Now().Add(liveOrderBound)
	for !spoolWatchPublished(dd, slowReq.Nonce) || !spoolWatchPublished(dd, behind.Nonce) {
		require.True(t, time.Now().Before(deadline),
			"idle passes never published the slow line and the spool behind it: slow=%v behind=%v, %d attempts, %d cut inside the slow part",
			spoolWatchPublished(dd, slowReq.Nonce), spoolWatchPublished(dd, behind.Nonce),
			counts.attempts.Load(), counts.cutInside.Load())
		ran, err := dd.idle.RunOnce(ctx, idleRunBudget)
		require.NoError(t, err)
		require.Contains(t, ran, idleTaskDrain, "fixture: the idle pass ran the drain")
	}
	require.Positive(t, counts.attempts.Load(), "fixture: an idle pass met the slow line")
	require.Zero(t, counts.cutInside.Load(),
		"no idle pass cancelled the slow line inside its slow part: the budget never cuts a line it started")
}

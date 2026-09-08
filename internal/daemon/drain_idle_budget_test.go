package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/paths"
)

// sp05d1IdleBudget is the wall-clock budget the idle tick hands RunOnce in this scenario. It is
// deliberately tiny: the interrupted handler blocks on its context rather than sleeping, so the
// budget's only job is to expire while that handler is mid-binding, and any positive value does
// that deterministically.
const sp05d1IdleBudget = 20 * time.Millisecond

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

			// The dispatcher acknowledges the first line and, while the budget is live, holds the
			// second until its context dies — that is the interrupted binding. Once the budget
			// has expired, every later dispatch is a healthy handler.
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
			ran, err := idle.RunOnce(context.Background(), sp05d1IdleBudget)
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

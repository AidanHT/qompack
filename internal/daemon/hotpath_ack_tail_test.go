package daemon

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/contract"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/obs"
)

// sp20d6WireDelay is the only part of B-A the daemon's own clock CAN see: the interval between the
// TS the client stamped on the request and the recvTS dispatchOp reads when the line arrives. It is
// advanced on the fake clock before dispatch, so recvTS - req.TS is exactly this value.
const sp20d6WireDelay = 4 * time.Millisecond

// sp20d6HandlerCost stands for everything the hot-path handler does between dispatchOp entering the
// route and the ipc server writing the ACK byte — at V5 that is ingest.Accept's durable WAL append,
// delivery lease and seal (SP20-D1), whose measured B-B p99 on this host is 36.864 ms. It is
// deliberately far larger than both that figure and the 15 ms B-A budget, so a sample that carried
// it could not possibly be mistaken for one that did not.
const sp20d6HandlerCost = 100 * time.Millisecond

// sp20d6DialBudget bounds the real-transport half of this test (connect, write, ACK read). It is a
// generous wall-clock ceiling on a loopback round trip, not a latency assertion: nothing this test
// pins is derived from it, and no assertion anywhere below compares a real duration to a threshold.
const sp20d6DialBudget = 10 * time.Second

// TestCarriedDefect_SP20D6_GatedBASampleExcludesThePreACKHandler is EVIDENCE for the carried defect
// SP20-D6. It pins the CURRENT behaviour, which is WRONG. A fix must invert the first subtest: the
// assertion that the recorded hook.controlled sample equals recvTS - req.TS + hotPathTailAllowance,
// and the one that it is strictly less than the handler's own cost, are what fail once the sample
// accounts for the region the hook actually waits on.
//
// What the contract says. 00-ARCHITECTURE.md:281 defines budget B-A as `hook_controlled — client
// main() entry → exit (connect + write + ACK)`, p99 < 15 ms, gated in CI. The ACK wait is INSIDE the
// budgeted region by that definition, not beside it.
//
// What is measured instead. internal/daemon/handlers.go calls recordHotPathSample AFTER callHandler
// has returned, but the value it records is computed only from the two timestamps that bracket the
// daemon's READ: `observed := time.Duration(int64(recvTS)-int64(req.TS)) * time.Millisecond`, plus
// hotPathTailAllowance. The handler's own duration is excluded by construction — it is never read
// from any clock, so no amount of work inside the route can move the sample. hotPathTailAllowance
// (internal/daemon/budget.go:14-26) is 1 ms, and its doc comment says it estimates "the ACK read plus
// process exit, after the daemon has stopped timing" and that over-counting is "the safe direction".
//
// Why that is wrong rather than merely approximate. internal/ipc/server.go runs
// `resp := s.dispatch(ctx, h, req)` BEFORE it writes the ACK byte or the response line, so the hook
// client is blocked on the full handler, not on the daemon's read. Since SP20-D1 — this wave — the
// observe.tool route runs the durable ingest.Accept (WAL append + fsync, lease journal, seal) inside
// that pre-ACK region; internal/config/deadlines.go's derivation comment records a measured B-B p99
// of 36.864 ms on this Windows host. So the hook's real main()→exit time is (B-A observed) + up to
// ~37 ms while the gated sample is (B-A observed) + 1 ms. The 1 ms allowance UNDER-counts the tail by
// the whole B-B region — the opposite of what budget.go's comment claims — and the §8.1 spool
// fallback (00-ARCHITECTURE.md:307-309: "B-A p99 exceeds budget for 3 consecutive 512-sample
// windows") therefore cannot fire on a breach the hook actually pays. internal/config/defaults.go
// sets HotPath.BudgetMs = 15 as a single value with no per-platform switch, so there is no Windows
// relaxation absorbing it either, as there is for B-B.
//
// Determinism. Nothing here sleeps, polls or compares a measured duration to a threshold. The
// "handler took 100 ms" is a fake-clock advance inside the route, and every ordering is a channel.
// The histogram reports Max exactly (internal/obs/hist.go tracks it in whole microseconds,
// independent of bucketing), so a single observation can be asserted to the nanosecond; the
// percentiles could not be, because they return a bucket's upper bound.
//
// What is NOT pinned here. The second subtest pins that the ACK byte is observed only after the
// handler returned, which is deterministic in the direction that holds today: the close of
// handlerReturned happens-before the handler's return, which happens-before the server's ACK write,
// which happens-before the client's read. Its NEGATIVE control is not deterministic — an
// implementation that ACKed before dispatching could still be scheduled such that the handler
// finished first — so that subtest is a statement of the current ordering, not the assertion that
// flips. The assertion that flips is the histogram equality in the first subtest.
func TestCarriedDefect_SP20D6_GatedBASampleExcludesThePreACKHandler(t *testing.T) {
	t.Run("the gated sample excludes the handler the hook waits on", func(t *testing.T) {
		root := t.TempDir()
		clk := newFakeClock(epoch)
		cfg := testConfig()

		d, err := New(Options{
			ProjectRoot: root, Cfg: cfg, Log: logging.Nop(), Clock: clk, Metrics: obs.New(clk),
		})
		require.NoError(t, err)
		dd, ok := d.(*daemon)
		require.True(t, ok)
		t.Cleanup(func() { _ = dd.ing.Close() })

		// The real observe.tool route, wrapped so that it ALSO advances the fake clock by
		// sp20d6HandlerCost before returning. Wrapping rather than replacing keeps ingest.Accept —
		// the work SP20-D1 put inside the pre-ACK region — on the path, and the advance stands for
		// its cost on a host where an fsync is slow. No production file is touched: dispatchOp reads
		// d.routes at call time, so the injection lives entirely in this test.
		route := dd.routes[ipc.OpObserveTool]
		require.NotNil(t, route, "observe.tool must be routed, or this test pins nothing")
		routeReturned := make(chan struct{})
		dd.routes[ipc.OpObserveTool] = func(ctx context.Context, req ipc.Request) ipc.Response {
			resp := route(ctx, req)
			clk.Advance(sp20d6HandlerCost)
			close(routeReturned)
			return resp
		}

		const sess core.SessionID = "sess-sp20d6-sample"
		sentTS := core.NowMilli(clk)
		clk.Advance(sp20d6WireDelay) // connect + write, the only part recvTS - req.TS can see

		resp := dd.dispatchOp(context.Background(), ipc.Request{
			Op: ipc.OpObserveTool, Session: sess, TS: sentTS,
			Event: &hookio.Event{
				HookEventName: "PostToolUse", SessionID: sess, CWD: root,
				TranscriptPath: filepath.Join(root, "t.jsonl"),
			},
		})
		require.True(t, resp.OK, "the hot-path request must be ACKed: %+v", resp)
		select {
		case <-routeReturned:
		default:
			t.Fatal("the observe.tool route never ran, so no pre-ACK handler cost was in play")
		}

		// The daemon's own lower bound is exactly the wire delay, as designed.
		lower := dd.m.Hist(histHookControlledObserved).Snapshot()
		require.Equal(t, int64(1), lower.N, "exactly one hot-path request was dispatched")
		require.Equal(t, sp20d6WireDelay, lower.Max,
			"hook_controlled_observed is recvTS - req.TS and nothing else")

		// SP20-D6: the GATED B-A series — the one §8.1's breach detector and CI read — is that same
		// lower bound plus a flat 1 ms, with the entire pre-ACK handler missing. This is the
		// assertion that flips when SP20-D6 is fixed: any fix that makes the sample account for the
		// region the hook actually blocks on (feeding the handler duration in, or re-defining B-A to
		// stop at daemon receipt) changes this value.
		gated := dd.m.Hist(histName(obs.BA)).Snapshot()
		require.Equal(t, int64(1), gated.N)
		require.Equal(t, sp20d6WireDelay+hotPathTailAllowance, gated.Max,
			"SP20-D6: the gated B-A sample is recvTS - req.TS + hotPathTailAllowance; the handler "+
				"the hook waits on contributes nothing to it")
		require.Less(t, gated.Max, sp20d6HandlerCost,
			"SP20-D6: the recorded sample is smaller than the handler's own cost alone, so the ACK "+
				"wait 00-ARCHITECTURE.md:281 puts inside B-A is not in the number B-A gates on")

		// The breach detector consumes that same under-counted estimate, which is why the §8.1
		// fallback cannot fire on this breach: the estimate is comfortably inside the budget while
		// the region the contract defines is many times over it.
		require.Len(t, dd.hotSamples, 1, "the estimate must reach the breach-detector channel")
		require.Equal(t, sp20d6WireDelay+hotPathTailAllowance, <-dd.hotSamples)

		budget := time.Duration(cfg.Runtime.HotPath.BudgetMs) * time.Millisecond
		require.Less(t, gated.Max, budget,
			"SP20-D6: the sample §8.1 gates on is inside budget B-A")
		require.Greater(t, sp20d6WireDelay+sp20d6HandlerCost, budget,
			"SP20-D6: while client main() entry -> exit, which 00-ARCHITECTURE.md:281 is the "+
				"definition of B-A, is over it — and no window of these samples can ever close a "+
				"breach, because the breach is not in them")
	})

	t.Run("the ack byte is observed only after the handler returned", func(t *testing.T) {
		root := t.TempDir()
		addr, err := ipc.Resolve(root)
		require.NoError(t, err)

		srv, err := ipc.NewServer(addr, logging.Nop(), nil, 0)
		require.NoError(t, err)
		t.Cleanup(func() { _ = srv.Close() })

		ctx, cancel := context.WithCancel(context.Background())
		t.Cleanup(cancel)

		handlerReturned := make(chan struct{})
		go func() {
			_ = srv.Serve(ctx, func(_ context.Context, _ ipc.Request) ipc.Response {
				close(handlerReturned)
				return ipc.Response{OK: true}
			})
		}()

		// A nil SpoolWriter: a Send that could not reach the server must drop rather than spool, so
		// the OK below can only have come from a real ACK byte.
		cl := ipc.NewClientWithOptions(addr, nil, logging.Nop(), nil, ipc.ClientOptions{
			State: ipc.State{
				Mode: contract.ModeFull, Hot: ipc.HotSync, DaemonEnabled: true,
			},
			ConnectDeadline: sp20d6DialBudget,
			AckDeadline:     sp20d6DialBudget,
		})
		t.Cleanup(func() { _ = cl.Close() })

		const sess core.SessionID = "sess-sp20d6-ack"
		resp, err := cl.Send(context.Background(), ipc.Request{
			Op: ipc.OpObserveTool, Session: sess, TS: core.NowMilli(core.SystemClock()),
			Event: &hookio.Event{
				HookEventName: "PostToolUse", SessionID: sess, CWD: root,
				TranscriptPath: filepath.Join(root, "t.jsonl"),
			},
		}, sp20d6DialBudget)
		require.NoError(t, err)
		require.True(t, resp.OK,
			"OK here means awaitACK actually read the ACK byte — every fallback route returns "+
				"OK:false — so what follows is an ordering statement about a real ACK")

		// internal/ipc/server.go's handleConn calls s.dispatch and only then writes the ACK, so the
		// close above happens-before the write, which happens-before this client's read returning.
		// A non-blocking receive is therefore guaranteed to succeed: the hook client paid for the
		// whole handler before Send came back. That is the cost budget B-A's own sample omits.
		select {
		case <-handlerReturned:
		default:
			t.Fatal("the ACK was observed before the handler returned, which would mean the pre-ACK " +
				"region SP20-D6 is about no longer exists")
		}
	})
}

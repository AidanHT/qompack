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

// TestCarriedDefect_SP20D6_GatedBASampleIncludesThePreACKHandler is the corrected
// regression for the old ExcludesThePreACKHandler identifier. A deterministic
// fake-clock handler delay must reach both the histogram and fallback detector;
// transport ordering is independently checked by the second subtest.
func TestCarriedDefect_SP20D6_GatedBASampleIncludesThePreACKHandler(t *testing.T) {
	t.Run("the gated sample includes the handler the hook waits on", func(t *testing.T) {
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

		gated := dd.m.Hist(histName(obs.BA)).Snapshot()
		require.Equal(t, int64(1), gated.N)
		expected := sp20d6WireDelay + sp20d6HandlerCost + hotPathTailAllowance
		require.Equal(t, expected, gated.Max, "the pre-ACK handler must be included")
		require.Len(t, dd.hotSamples, 1)
		require.Equal(t, expected, <-dd.hotSamples)
		budget := time.Duration(cfg.Runtime.HotPath.BudgetMs) * time.Millisecond
		require.Greater(t, gated.Max, budget, "the fallback detector must receive the actual breach")
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

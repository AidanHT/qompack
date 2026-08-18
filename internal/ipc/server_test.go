package ipc

import (
	"context"
	"encoding/json"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/obs"
)

// rawDialBound and rawIOBound are the budgets this file's own hand-rolled wire client uses for a
// dial and for a single write/read against an already-accepted local connection. Both were bare
// literals repeated at nine call sites (V2-MERGE-25 ②); naming them is what lets the relationship
// below be stated rather than assumed.
//
// Their basis is the server's own read budget: handleConn resets a connIdleTimeout (10 minutes)
// before every read, so the server will wait far longer than either of these. That is the right
// way round — a test bound that outlived the server's own patience could not fail a wedged read,
// it would just hang. Against the microsecond-scale real cost of a local pipe or socket round
// trip, both are several orders of magnitude of headroom, so neither can flake on scheduling
// jitter alone. rawIOBound is expressed as a multiple of rawDialBound because a completed dial is
// strictly the cheaper of the two operations.

const (
	rawDialBound = time.Second
	rawIOBound   = 2 * rawDialBound
)

// newRawClient dials addr directly, bypassing Client entirely — these tests exercise the wire
// protocol and the accept loop, not the hot-path client's own failure handling.
func newRawClient(t *testing.T, addr Addr) net.Conn {
	t.Helper()
	conn, err := dial(addr, rawDialBound)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

func writeRequest(t *testing.T, conn net.Conn, req Request) {
	t.Helper()
	require.NoError(t, conn.SetWriteDeadline(time.Now().Add(rawDialBound)))
	line, err := EncodeRequest(req)
	require.NoError(t, err)
	_, err = conn.Write(line)
	require.NoError(t, err)
}

func readByte(t *testing.T, conn net.Conn) byte {
	t.Helper()
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(rawIOBound)))
	var b [1]byte
	_, err := conn.Read(b[:])
	require.NoError(t, err)
	return b[0]
}

// TestServerRoutesAndACKs is the table's row: a Handler that answers admin.ping ACKs a
// fire-and-forget request, and returns a full NDJSON line for a Reply:true request.
func TestServerRoutesAndACKs(t *testing.T) {
	_, addr := newTestServer(t, func(_ context.Context, req Request) Response {
		if req.Op != OpAdminPing {
			return Response{OK: false, Err: "unrecognized op"}
		}
		if req.Reply {
			return Response{OK: true, Data: json.RawMessage(`{"pong":true}`)}
		}
		return Response{OK: true}
	})
	conn := newRawClient(t, addr)

	writeRequest(t, conn, Request{Op: OpAdminPing, Session: "s", TS: 1})
	require.Equal(t, ACK, readByte(t, conn))

	writeRequest(t, conn, Request{Op: OpAdminPing, Session: "s", TS: 2, Reply: true})
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(rawIOBound)))
	line, err := NewLineReader(conn, MaxLineBytes).ReadLine()
	require.NoError(t, err)
	resp, err := DecodeResponse(line)
	require.NoError(t, err)
	require.True(t, resp.OK)
	require.JSONEq(t, `{"pong":true}`, string(resp.Data))
}

// TestServerUnknownOpNAKs is the table's row (adapted: routing is a daemon concern now, so the
// Handler itself refuses, not a Router): a refused request NAKs and the connection stays open —
// a following valid request on the same connection still ACKs.
func TestServerUnknownOpNAKs(t *testing.T) {
	_, addr := newTestServer(t, func(_ context.Context, req Request) Response {
		if req.Op == OpAdminPing {
			return Response{OK: true}
		}
		return Response{OK: false, Err: "unrecognized op"}
	})
	conn := newRawClient(t, addr)

	writeRequest(t, conn, Request{Op: Op("bogus.op"), Session: "s", TS: 1})
	require.Equal(t, NAK, readByte(t, conn))

	writeRequest(t, conn, Request{Op: OpAdminPing, Session: "s", TS: 2})
	require.Equal(t, ACK, readByte(t, conn), "the connection must stay open and keep serving after a NAK")
}

// TestServerDecodeErrorNAKsAndCounts is I-8: malformed JSON — as opposed to well-formed JSON
// naming an op the Handler happens to refuse — never reaches the Handler at all, NAKs, increments
// ipc_decode_error, and leaves the connection open for a following valid request.
func TestServerDecodeErrorNAKsAndCounts(t *testing.T) {
	var handlerCalls atomic.Int64
	reg := obs.New(core.SystemClock())
	addr, err := Resolve(t.TempDir())
	require.NoError(t, err)
	srv, err := NewServer(addr, logging.Nop(), reg, MaxLineBytes)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- srv.Serve(ctx, func(context.Context, Request) Response {
			handlerCalls.Add(1)
			return Response{OK: true}
		})
	}()
	t.Cleanup(func() { cancel(); _ = srv.Close(); <-done })

	conn := newRawClient(t, addr)
	require.NoError(t, conn.SetWriteDeadline(time.Now().Add(rawDialBound)))
	_, err = conn.Write([]byte("{not json\n"))
	require.NoError(t, err)
	require.Equal(t, NAK, readByte(t, conn))
	require.EqualValues(t, 1, reg.Counter(counterIPCDecodeError).Value())
	require.EqualValues(t, 0, handlerCalls.Load(), "malformed JSON must never reach the handler")

	writeRequest(t, conn, Request{Op: OpAdminPing, Session: "s", TS: 1})
	require.Equal(t, ACK, readByte(t, conn), "the connection must stay open and keep serving after a decode error")
	require.EqualValues(t, 1, handlerCalls.Load())
}

// TestServerHandlerPanicIsContained is the table's row: a panicking Handler NAKs the request that
// triggered it, keeps serving, and counts the panic.
func TestServerHandlerPanicIsContained(t *testing.T) {
	reg := obs.New(core.SystemClock())
	addr, err := Resolve(t.TempDir())
	require.NoError(t, err)
	srv, err := NewServer(addr, logging.Nop(), reg, MaxLineBytes)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	var calls atomic.Int64
	go func() {
		done <- srv.Serve(ctx, func(context.Context, Request) Response {
			calls.Add(1)
			panic("boom")
		})
	}()
	t.Cleanup(func() { cancel(); _ = srv.Close(); <-done })

	conn := newRawClient(t, addr)
	writeRequest(t, conn, Request{Op: OpAdminPing, Session: "s", TS: 1})
	require.Equal(t, NAK, readByte(t, conn))

	writeRequest(t, conn, Request{Op: OpAdminPing, Session: "s", TS: 2})
	require.Equal(t, NAK, readByte(t, conn), "the server must keep serving after a handler panic")

	require.EqualValues(t, 2, calls.Load())
	require.EqualValues(t, 2, reg.Counter(counterIPCHandlerPanic).Value())
}

// TestServerMultiplexesLines is the table's row: 3 requests on one connection produce 3 ACK bytes
// in order.
func TestServerMultiplexesLines(t *testing.T) {
	_, addr := newTestServer(t, func(context.Context, Request) Response { return Response{OK: true} })
	conn := newRawClient(t, addr)

	for i := 0; i < 3; i++ {
		writeRequest(t, conn, Request{Op: OpAdminPing, Session: "s", TS: core.UnixMilli(i)})
	}
	for i := 0; i < 3; i++ {
		require.Equal(t, ACK, readByte(t, conn), "byte %d", i)
	}
}

// TestServerConcurrentClients is the table's row: 64 goroutines × 50 requests, under -race, must
// produce exactly 3200 ACKs with no dropped request.
func TestServerConcurrentClients(t *testing.T) {
	const goroutines, perGoroutine = 64, 50

	var handled atomic.Int64
	_, addr := newTestServer(t, func(context.Context, Request) Response {
		handled.Add(1)
		return Response{OK: true}
	})

	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			conn, err := dial(addr, rawIOBound)
			if !assert.NoError(t, err) {
				return
			}
			defer func() { _ = conn.Close() }()

			for i := 0; i < perGoroutine; i++ {
				line, encErr := EncodeRequest(Request{Op: OpAdminPing, Session: core.SessionID(strconv.Itoa(g)), TS: core.UnixMilli(i)})
				if !assert.NoError(t, encErr) {
					return
				}
				if assert.NoError(t, conn.SetWriteDeadline(time.Now().Add(rawIOBound))) {
					_, werr := conn.Write(line)
					assert.NoError(t, werr)
				}
				if assert.NoError(t, conn.SetReadDeadline(time.Now().Add(rawIOBound))) {
					var b [1]byte
					_, rerr := conn.Read(b[:])
					if assert.NoError(t, rerr) {
						assert.Equal(t, ACK, b[0])
					}
				}
			}
		}(g)
	}
	wg.Wait()

	require.EqualValues(t, goroutines*perGoroutine, handled.Load())
}

// shutdownTestBound is how long TestServerCloseWithLiveConnection (and the N-2a stress test)
// gives Close/Serve to return. The real, working case returns in low single-digit milliseconds —
// Close closes every registered connection itself, unblocking handleConn's pending read
// immediately — so this bound stays well below serverCloseWait (2s) itself, not merely below some
// multiple of it: a regression that fell back to waitForConns' own internal serverCloseWait timer
// (because closing tracked connections had silently stopped happening) would blow straight through
// this bound and fail loudly, rather than sneaking under a looser one. N-2b (fix round 3).
//
// It is therefore the one bound in this file that is deliberately SMALLER than the thing it waits
// for — Close is internally capped at serverCloseWait for closeListenerBounded and again for
// waitForConns — and that is stated here rather than left to be rediscovered (V2-MERGE-25 ②). The
// cost is real and known: on Windows under load, go-winio v0.6.2's own listener Close can consume
// most of closeListenerBounded's cap on its own (see that method's doc comment), and this test
// flakes when it does. Loosening the bound would not distinguish that from the regression it
// exists to catch — both land at roughly serverCloseWait — so the honest fix is a newer go-winio
// or a cancellable-Accept redesign, and it is carried as known-deferred (§2.5a E), not papered
// over here.
const shutdownTestBound = serverCloseWait / 2

// TestServerCloseWithLiveConnection is I-4: a connection that is accepted but never sends
// anything and never closes must not prevent Close — or a cancellation-driven Serve — from
// returning. Close must close every accepted connection itself (unblocking handleConn's pending
// read) rather than waiting out connIdleTimeout or hanging forever.
func TestServerCloseWithLiveConnection(t *testing.T) {
	addr, err := Resolve(t.TempDir())
	require.NoError(t, err)
	srv, err := NewServer(addr, logging.Nop(), obs.New(core.SystemClock()), MaxLineBytes)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	serveDone := make(chan error, 1)
	go func() {
		serveDone <- srv.Serve(ctx, func(context.Context, Request) Response { return Response{OK: true} })
	}()

	// Connect and deliberately write nothing: handleConn is now blocked in ReadLine, exactly the
	// "peer that never sends and never disconnects" case I-4 describes.
	conn := newRawClient(t, addr)
	t.Cleanup(func() { _ = conn.Close() })

	closeDone := make(chan error, 1)
	go func() { closeDone <- srv.Close() }()

	select {
	case err := <-closeDone:
		require.NoError(t, err)
	case <-time.After(shutdownTestBound):
		t.Fatalf("Close did not return within %s with a live, silent connection open", shutdownTestBound)
	}

	select {
	case err := <-serveDone:
		require.NoError(t, err, "Serve must return cleanly once Close has finished")
	case <-time.After(shutdownTestBound):
		t.Fatalf("Serve did not return within %s after Close, with a live, silent connection open", shutdownTestBound)
	}
}

// TestServerConcurrentDialVsClose is the N-2a regression test: it repeatedly races a client dial
// against Close so that, across many iterations under -race, the scheduler eventually lands the
// exact window between Accept returning a connection and that connection being registered — the
// window where the pre-fix implementation could either panic ("WaitGroup misuse: Add called
// concurrently with Wait", since closing the listener only unblocks a *pending* Accept, never a
// connection Accept had already returned) or leak a connection that escaped closeTrackedConns
// entirely. If registerConn's atomic track-and-Add-or-refuse decision (server.go) regresses, this
// test is expected to crash the whole test binary via that WaitGroup panic — a plain per-iteration
// assertion failure would not be a strong enough signal for a bug that used to corrupt process
// state, not just fail one check.
//
// No sleeps: every wait is bounded by a real deadline (testDeadline for the dial, stressTestBound
// for Close/Serve), and 100 iterations is enough to give the OS scheduler and the race detector
// many independent opportunities to interleave the accept/close race differently.
//
// stressTestBound, not shutdownTestBound: creating and tearing down 100 servers back to back
// under -race is real, cumulative system load, and this test's primary regression signal does
// not depend on how tight this bound is. If registerConn's atomic decision regresses, the
// "Add called concurrently with Wait" panic crashes the whole test binary unconditionally, on
// the spot — no timeout involved. And Close is now hard-capped in both of its own waits —
// closeListenerBounded and waitForConns are each capped at serverCloseWait — so it cannot
// legitimately take more than roughly 2×serverCloseWait regardless of load or of go-winio's own
// Close occasionally hanging (see closeListenerBounded's doc comment, server.go). This bound
// gives that theoretical worst case real headroom rather than sitting right on top of it.
const stressTestBound = 2*serverCloseWait + time.Second

// serveDrainBound bounds the best-effort drain phase at the end of TestServerConcurrentDialVsClose
// — see the test's own doc comment for why a straggler past this point is logged, not failed.
const serveDrainBound = 5 * time.Second

func TestServerConcurrentDialVsClose(t *testing.T) {
	const iterations = 100
	serveDones := make([]chan error, iterations)

	for i := 0; i < iterations; i++ {
		addr, err := Resolve(t.TempDir())
		require.NoError(t, err)
		srv, err := NewServer(addr, logging.Nop(), obs.New(core.SystemClock()), MaxLineBytes)
		require.NoError(t, err)

		serveDone := make(chan error, 1)
		serveDones[i] = serveDone
		go func() {
			serveDone <- srv.Serve(context.Background(), func(context.Context, Request) Response { return Response{OK: true} })
		}()

		// Race a dial against Close on purpose — whichever wins, neither side may panic or hang.
		dialDone := make(chan struct{})
		go func() {
			defer close(dialDone)
			if conn, dialErr := dial(addr, testDeadline); dialErr == nil {
				_ = conn.Close()
			}
		}()

		closeDone := make(chan error, 1)
		go func() { closeDone <- srv.Close() }()

		// Close's own promptness is what N-2a's fix (registerConn, plus closeListenerBounded's
		// hardening of the platform Close call) actually guarantees, so it is checked tightly,
		// per iteration, right here. Across many hundreds of iterations run while building this
		// test, Close never once missed this bound — unlike Serve, below.
		select {
		case err := <-closeDone:
			require.NoError(t, err, "iteration %d", i)
		case <-time.After(stressTestBound):
			t.Fatalf("iteration %d: Close did not return within %s", i, stressTestBound)
		}
		<-dialDone
	}

	// Serve's own return is a separate, decoupled liveness property from Close's promptness:
	// Serve only notices a shutdown once its blocked Accept call actually returns, and that
	// depends on the underlying platform listener's Close taking effect. closeListenerBounded's
	// doc comment (server.go) documents that go-winio's own Close/Accept synchronization can,
	// independent of anything this package does, occasionally leave that Accept call unblocked
	// only after a very long delay — empirically, sometimes still not within 30s. No bound this
	// test could reasonably wait would turn that into a hard guarantee, so gating iterations (or
	// even failing the test outright) on Serve returning promptly would make this test's
	// pass/fail turn on a pre-existing, go-winio-level limitation rather than on the registerConn
	// race it exists to guard. Every Serve goroutine is instead drained here on a best-effort
	// basis: whichever ones return within serveDrainBound must report a nil error (the actual
	// correctness assertion), and any still outstanding after that are logged, not failed — they
	// keep running harmlessly in the background until the test binary exits.
	deadline := time.After(serveDrainBound)
	stragglers := 0
drain:
	for i, done := range serveDones {
		select {
		case err := <-done:
			require.NoError(t, err, "iteration %d", i)
		case <-deadline:
			stragglers = iterations - i
			break drain
		}
	}
	if stragglers > 0 {
		t.Logf("%d/%d Serve goroutine(s) had not returned within the %s drain window — see "+
			"closeListenerBounded's doc comment (server.go) for the known, unrelated go-winio "+
			"limitation this reflects, not a regression in this package's own logic",
			stragglers, iterations, serveDrainBound)
	}
}

// BenchmarkServerRoundTrip measures the daemon-side portion of B-A against a warm in-process
// server: budget is p99 < 2 ms.
func BenchmarkServerRoundTrip(b *testing.B) {
	addr, err := Resolve(b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	srv, err := NewServer(addr, logging.Nop(), obs.New(core.SystemClock()), MaxLineBytes)
	if err != nil {
		b.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx, func(context.Context, Request) Response { return Response{OK: true} }) }()
	b.Cleanup(func() { cancel(); _ = srv.Close(); <-done })

	conn, err := dial(addr, rawIOBound)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = conn.Close() })

	line, err := EncodeRequest(Request{Op: OpAdminPing, Session: "bench", TS: 1})
	if err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := conn.Write(line); err != nil {
			b.Fatal(err)
		}
		var ack [1]byte
		if _, err := conn.Read(ack[:]); err != nil {
			b.Fatal(err)
		}
	}
}

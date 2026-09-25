package ipc

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/obs"
)

// The V6 close-out linux lane's N1: the admin.shutdown reply could be lost. The daemon's handler
// starts Stop on a goroutine and returns its reply; Stop's first act cancels the context Serve runs
// under, Serve's context.AfterFunc calls Close, and Close used to close every tracked connection —
// including the one whose reply the handler was about to write. One Linux -race run in 100 saw
// Send return OK:false in 0.15 s (plans/sdd/V6-closeout/linux/report.md §9 N1).
//
// These tests inject that ordering deterministically instead of waiting for the scheduler to find
// it: the handler itself triggers the shutdown and does not return until Close has swept the
// tracked connections. Before the fix, that sweep had already closed the handler's own connection.

// inflightTestBound bounds every wait below. Close's own worst case is about 2×serverCloseWait
// (closeListenerBounded plus waitForConns), so this is headroom over the server's own bounds, not a
// latency expectation.
const inflightTestBound = 2*serverCloseWait + time.Second

// waitClosing spins until Close has begun, which — because Close sets closing and sweeps the
// tracked connections in one connsMu critical section — also means the sweep has finished. It
// yields rather than sleeps, and reports false if the bound passes first.
func waitClosing(s *server, bound time.Duration) bool {
	deadline := time.Now().Add(bound)
	for !s.isClosing() {
		if time.Now().After(deadline) {
			return false
		}
		runtime.Gosched()
	}
	return true
}

// newInflightServer starts a server whose handler cancels Serve's own context — exactly what
// daemon.Stop's runCancel does to the daemon's Serve — waits until the shutdown has swept the
// connections, and only then answers.
func newInflightServer(t *testing.T, reply Response) (*server, Addr, chan error) {
	t.Helper()
	addr, err := Resolve(t.TempDir())
	require.NoError(t, err)
	srvI, err := NewServer(addr, logging.Nop(), obs.New(core.SystemClock()), MaxLineBytes)
	require.NoError(t, err)
	srv, ok := srvI.(*server)
	require.True(t, ok)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	serveDone := make(chan error, 1)
	go func() {
		serveDone <- srv.Serve(ctx, func(context.Context, Request) Response {
			cancel() // the shutdown this request announces, as admin.shutdown's Stop does
			if !waitClosing(srv, inflightTestBound) {
				return Response{OK: false, Err: "test: Close never began"}
			}
			return reply
		})
	}()
	return srv, addr, serveDone
}

// TestServerCloseLetsAnInFlightReplyFinish is N1's regression: a Reply request whose handler is
// running when Close begins still gets its full response line, and the connection is closed right
// after it — a shutting-down server serves no second request on it.
func TestServerCloseLetsAnInFlightReplyFinish(t *testing.T) {
	_, addr, serveDone := newInflightServer(t, Response{OK: true, Data: json.RawMessage(`{"stopping":true}`)})
	conn := newRawClient(t, addr)

	writeRequest(t, conn, Request{Op: OpAdminShutdown, Reply: true})
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(inflightTestBound)))
	lr := NewLineReader(conn, MaxLineBytes)
	line, err := lr.ReadLine()
	require.NoError(t, err, "the reply of a request in flight when Close began was lost")
	resp, err := DecodeResponse(line)
	require.NoError(t, err)
	require.True(t, resp.OK)
	require.JSONEq(t, `{"stopping":true}`, string(resp.Data))

	_, err = lr.ReadLine()
	require.Error(t, err, "a closing server must close the connection after its last reply")
	require.False(t, isTimeout(err), "the connection must be closed, not merely idle: %v", err)

	select {
	case err := <-serveDone:
		require.ErrorIs(t, err, context.Canceled, "a cancellation-driven Serve reports ctx's error")
	case <-time.After(inflightTestBound):
		t.Fatal("Serve did not return after the in-flight reply finished")
	}
}

// TestServerCloseLetsAnInFlightACKFinish is the same ordering for a fire-and-forget request: its
// one-byte ACK is its reply, and a lost ACK makes a client spool an event the daemon already took.
func TestServerCloseLetsAnInFlightACKFinish(t *testing.T) {
	_, addr, serveDone := newInflightServer(t, Response{OK: true})
	conn := newRawClient(t, addr)

	writeRequest(t, conn, Request{Op: OpAdminPing, Session: "s", TS: 1})
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(inflightTestBound)))
	var b [1]byte
	_, err := conn.Read(b[:])
	require.NoError(t, err, "the ACK of a request in flight when Close began was lost")
	require.Equal(t, ACK, b[0])

	select {
	case <-serveDone:
	case <-time.After(inflightTestBound):
		t.Fatal("Serve did not return after the in-flight ACK finished")
	}
}

// TestServerCloseStillClosesIdleConnections pins the half of Close that must not change: a
// connection with no request in flight is closed at once, so a silent peer cannot hold the
// shutdown open (I-4), even while another connection's reply is still being finished.
func TestServerCloseStillClosesIdleConnections(t *testing.T) {
	_, addr, serveDone := newInflightServer(t, Response{OK: true})
	idle := newRawClient(t, addr)
	busy := newRawClient(t, addr)

	writeRequest(t, busy, Request{Op: OpAdminShutdown, Reply: true})
	require.NoError(t, busy.SetReadDeadline(time.Now().Add(inflightTestBound)))
	_, err := NewLineReader(busy, MaxLineBytes).ReadLine()
	require.NoError(t, err)

	require.NoError(t, idle.SetReadDeadline(time.Now().Add(inflightTestBound)))
	var b [1]byte
	_, err = idle.Read(b[:])
	require.Error(t, err, "an idle connection must be closed by Close")
	require.False(t, isTimeout(err), "the idle connection was left open until the test's own bound: %v", err)

	select {
	case <-serveDone:
	case <-time.After(inflightTestBound):
		t.Fatal("Serve did not return")
	}
}

// TestServerCloseBoundsAReplyNobodyReads is the "bounded" half of the fix: a finishing reply is
// written under a write deadline, so a peer that sent a request and then never reads cannot hold
// the connection's goroutine open forever. The reply is larger than the transport's buffers so the
// write really blocks.
func TestServerCloseBoundsAReplyNobodyReads(t *testing.T) {
	big := make([]byte, 4*pipeTestBufferBytes)
	for i := range big {
		big[i] = 'x'
	}
	data, err := json.Marshal(string(big))
	require.NoError(t, err)
	srv, addr, serveDone := newInflightServer(t, Response{OK: true, Data: data})
	conn := newRawClient(t, addr)

	writeRequest(t, conn, Request{Op: OpAdminShutdown, Reply: true})
	// Deliberately never read.

	select {
	case <-serveDone:
	case <-time.After(inflightTestBound):
		t.Fatal("Serve did not return")
	}
	deadline := time.Now().Add(inflightTestBound)
	for srv.trackedConns() != 0 {
		if time.Now().After(deadline) {
			t.Fatalf("a reply nobody reads kept its connection open past %s", inflightTestBound)
		}
		runtime.Gosched()
	}
}

// pipeTestBufferBytes is larger than any transport buffer this package configures (the Windows
// pipe's 64 KiB hint; a Unix socket's default is smaller), so a reply four times its size cannot be
// absorbed by the kernel and its write blocks until the peer reads.
const pipeTestBufferBytes = 256 << 10

// isTimeout reports whether err is a deadline expiry rather than a closed connection.
func isTimeout(err error) bool {
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return true
	}
	return false
}

// trackedConns reports how many connections the server still tracks: a handleConn goroutine
// untracks its connection as its very last act.
func (s *server) trackedConns() int {
	s.connsMu.Lock()
	defer s.connsMu.Unlock()
	return len(s.conns)
}

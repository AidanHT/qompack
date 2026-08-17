package ipc

import (
	"context"
	"errors"
	"net"
	"sync"
	"time"

	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/obs"
)

// counterIPCDecodeError and counterIPCHandlerPanic are the two server-side obs.Counter names
// (repo underscore idiom, not §2.4's dotted prose spelling — Task-2 controller ruling, kept
// consistent because later tasks assert these exact strings).
const (
	counterIPCDecodeError  = "ipc_decode_error"
	counterIPCHandlerPanic = "ipc_handler_panic"
)

// acceptBackoffInitial and acceptBackoffMax bound the accept loop's retry delay after a temporary
// Accept error (too many open files, etc.): start small, double, cap — driven by a timer inside a
// select, never time.Sleep (§6.1).
const (
	acceptBackoffInitial = 5 * time.Millisecond
	acceptBackoffMax     = 1 * time.Second
)

// serverCloseWait bounds how long Close (and a cancellation-driven Serve return) waits for
// in-flight connections to finish before giving up anyway — an orderly shutdown should not hang
// the daemon's own stop sequence forever on one slow or wedged client.
const serverCloseWait = 2 * time.Second

// connIdleTimeout bounds how long handleConn will block in one ReadLine call with no data from
// the peer. It is a backstop against a connection that never sends and never closes — Close
// itself closes every tracked connection outright, which is the primary defence, but a read
// deadline means a single idle peer cannot wedge its own goroutine indefinitely even absent a
// Close call. It is generous enough not to disrupt a legitimately idle long-lived connection
// (MCP-over-daemon multiplexing, per this file's own handleConn doc comment).
const connIdleTimeout = 10 * time.Minute

// server is the real Server (00-ARCHITECTURE.md §5.4): the daemon-side half of the transport.
type server struct {
	addr    Addr
	ln      net.Listener
	log     logging.Logger
	m       obs.Registry
	maxLine int

	wg sync.WaitGroup

	// connsMu guards conns and closing together, as one atomic decision, which is what closes the
	// N-2a race: Accept can return a live connection to Serve's goroutine at the exact moment
	// another goroutine is inside Close, and closing the listener only unblocks a *pending*
	// Accept — it does nothing for a connection Accept has already returned. Without a single
	// lock covering both "is this connection tracked" and "have we started shutting down",
	// Serve's wg.Add(1) for that connection could land after Close's wg.Wait() has already
	// registered as a waiter on a zero counter, which is the exact precondition sync.WaitGroup's
	// own runtime check panics on ("WaitGroup misuse: Add called concurrently with Wait") — and,
	// short of that panic, the same window could leave the connection in neither Close's swept set
	// nor wg's count, leaking it until it eventually times out on its own via connIdleTimeout.
	//
	// registerConn is the only place that calls wg.Add, and it does so under this same lock,
	// after checking closing — so a connection is either added to conns and counted in wg
	// atomically with respect to Close's own critical section, or Close has already run (closing
	// is true) and registerConn refuses it outright, leaving wg untouched. Close's critical
	// section sets closing, closes the listener, and closes every already-tracked connection, all
	// under the same lock, so nothing accepted concurrently with Close can be missed by either its
	// close sweep or by never having been Added in the first place.
	connsMu sync.Mutex
	conns   map[net.Conn]struct{}
	closing bool

	closeOnce sync.Once
	closeErr  error
}

// NewServer binds a.Path and returns a Server ready for Serve. maxLine <= 0 defaults to
// MaxLineBytes.
func NewServer(a Addr, log logging.Logger, m obs.Registry, maxLine int) (Server, error) {
	if log == nil {
		log = logging.Nop()
	}
	if maxLine <= 0 {
		maxLine = MaxLineBytes
	}
	ln, err := listen(a, log, maxLine)
	if err != nil {
		return nil, err
	}
	return &server{addr: a, ln: ln, log: log, m: m, maxLine: maxLine, conns: make(map[net.Conn]struct{})}, nil
}

// Addr reports the endpoint this Server is bound to. It is stable for the Server's lifetime.
func (s *server) Addr() Addr { return s.addr }

// Serve runs the accept loop until ctx is cancelled or Close is called, dispatching every request
// on every connection to h — a connection is read until EOF, so one long-lived caller (MCP-over-
// daemon) can send many requests without reconnecting. It returns nil on an orderly shutdown
// triggered by Close, or ctx's own error when the shutdown was triggered by cancellation.
func (s *server) Serve(ctx context.Context, h Handler) error {
	if h == nil {
		h = func(context.Context, Request) Response { return Response{OK: false, Err: "ipc: no handler"} }
	}

	stop := context.AfterFunc(ctx, func() { _ = s.Close() })
	defer stop()

	backoff := acceptBackoffInitial
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			if s.isClosing() {
				s.waitForConns()
				if ctx.Err() != nil {
					return ctx.Err()
				}
				return nil
			}

			var ne net.Error
			if errors.As(err, &ne) && ne.Temporary() { //nolint:staticcheck // SA1019: the documented accept-loop retry idiom this package's own spec calls for
				if !s.waitBackoff(ctx, &backoff) {
					s.waitForConns()
					return ctx.Err()
				}
				continue
			}
			s.waitForConns()
			return err
		}
		backoff = acceptBackoffInitial
		// registerConn is the one place that decides, under connsMu, whether this connection is
		// admitted at all — see the struct's own doc comment for why that has to be one atomic
		// decision with Close rather than two separate steps (track, then wg.Add).
		if !s.registerConn(conn) {
			// Close won the race: it had already set closing (and swept every connection tracked
			// at that instant) before this Accept's connection could be registered. Refuse it
			// outright — no tracking, no wg.Add, nothing for Close to have missed.
			_ = conn.Close()
			continue
		}
		go s.handleConn(ctx, conn, h)
	}
}

// waitForConns waits for every in-flight handleConn goroutine to finish, bounded by
// serverCloseWait — the same bound Close itself uses — so neither Serve nor Close can hang the
// daemon's stop sequence forever on a single wedged connection.
//
// Serve's own shutdown paths and an independently-invoked Close can both reach this concurrently.
// That used to be reasoned about as "safe because every wg.Add happens before Wait" — which was
// wrong (N-2a): closing the listener only unblocks a *pending* Accept, not a connection Accept
// had already returned, so a wg.Add for that connection could still be about to happen after this
// call's wg.Wait() had already registered as a waiter on a zero counter. That specific shape —
// Add taking the counter from zero to positive while a Wait is already blocked — is exactly what
// sync.WaitGroup's own runtime check panics on. The actual fix is not here: registerConn makes
// wg.Add and the "are we closing" check one atomic decision under connsMu (see the struct's doc
// comment), so by the time any caller reaches this function, every wg.Add that could still happen
// has already been refused by registerConn or has already completed — there is nothing left in
// flight for a fresh Add to race against a Wait over.
func (s *server) waitForConns() {
	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()
	t := time.NewTimer(serverCloseWait)
	defer t.Stop()
	select {
	case <-done:
	case <-t.C:
	}
}

// waitBackoff waits for the current backoff duration (doubling it, capped at acceptBackoffMax)
// or for ctx to end, whichever comes first. It reports whether the wait completed normally.
func (s *server) waitBackoff(ctx context.Context, backoff *time.Duration) bool {
	t := time.NewTimer(*backoff)
	defer t.Stop()
	select {
	case <-t.C:
		*backoff *= 2
		if *backoff > acceptBackoffMax {
			*backoff = acceptBackoffMax
		}
		return true
	case <-ctx.Done():
		return false
	}
}

// handleConn reads NDJSON frames off conn until EOF or a fatal I/O error, dispatching each to h
// and writing back the one-byte ACK/NAK or, for a Reply request, a full response line.
func (s *server) handleConn(ctx context.Context, conn net.Conn, h Handler) {
	defer s.unregisterConn(conn)
	defer func() { _ = conn.Close() }()

	lr := NewLineReader(conn, s.maxLine)
	for {
		// Reset before every read: a connIdleTimeout budget per frame, not for the connection's
		// whole lifetime, so a long-lived multiplexed connection that is merely quiet between
		// requests is never penalised for the traffic it already sent.
		if err := conn.SetReadDeadline(time.Now().Add(connIdleTimeout)); err != nil {
			return
		}
		line, err := lr.ReadLine()
		if err != nil {
			if errors.Is(err, ErrLineTooLong) {
				// The oversize line was already discarded by ReadLine; NAK it and keep reading —
				// it must never reach the handler.
				if !s.writeByte(conn, NAK) {
					return
				}
				continue
			}
			return // clean EOF or a real I/O error: this connection is done
		}

		req, decErr := DecodeRequest(line)
		if decErr != nil {
			if s.m != nil {
				s.m.Counter(counterIPCDecodeError).Add(1)
			}
			if !s.writeByte(conn, NAK) {
				return
			}
			continue
		}

		resp := s.dispatch(ctx, h, req)

		if req.Reply {
			out, encErr := EncodeResponse(resp)
			if encErr != nil {
				return
			}
			if _, werr := conn.Write(out); werr != nil {
				return
			}
			continue
		}

		b := NAK
		if resp.OK {
			b = ACK
		}
		if !s.writeByte(conn, b) {
			return
		}
	}
}

// dispatch calls h, containing a panic so one hostile or buggy handler invocation never brings
// down the daemon's read loop: the connection gets a NAK and keeps serving, and the panic is
// counted and logged Loud rather than silently swallowed.
func (s *server) dispatch(ctx context.Context, h Handler, req Request) (resp Response) {
	defer func() {
		if r := recover(); r != nil {
			if s.m != nil {
				s.m.Counter(counterIPCHandlerPanic).Add(1)
			}
			if s.log != nil {
				s.log.Loud("ipc: handler panicked — request refused", "op", string(req.Op), "recover", r)
			}
			resp = Response{OK: false, Err: "ipc: handler panic"}
		}
	}()
	return h(ctx, req)
}

// writeByte writes a single control byte (ACK or NAK) and reports whether the write succeeded.
func (s *server) writeByte(conn net.Conn, b byte) bool {
	_, err := conn.Write([]byte{b})
	return err == nil
}

// isClosing reports whether Close has begun (its critical section may still be running or may
// have already finished — either way, closing is true from the moment that section starts).
func (s *server) isClosing() bool {
	s.connsMu.Lock()
	defer s.connsMu.Unlock()
	return s.closing
}

// registerConn is the sole gate a newly-accepted connection passes through before Serve spawns a
// handleConn goroutine for it. It reports false — refusing the connection outright, with no
// tracking and no wg.Add — if Close has already begun; otherwise it tracks conn and increments wg
// in the same critical section, so Close can never observe "nothing to track, nothing to wait
// for" while this connection is still about to be counted (N-2a; see the struct's doc comment).
func (s *server) registerConn(conn net.Conn) bool {
	s.connsMu.Lock()
	defer s.connsMu.Unlock()
	if s.closing {
		return false
	}
	s.conns[conn] = struct{}{}
	s.wg.Add(1)
	return true
}

// unregisterConn is registerConn's inverse, called once by handleConn's own deferred cleanup:
// untrack conn and mark its wg slot done, together, so a concurrent Close's view of "what is
// still tracked" and "what wg is still waiting for" never disagree.
func (s *server) unregisterConn(conn net.Conn) {
	s.connsMu.Lock()
	delete(s.conns, conn)
	s.connsMu.Unlock()
	s.wg.Done()
}

// Close stops Serve (by closing the listener, which unblocks a pending Accept — though not a
// connection Accept has already returned, which is what registerConn's refusal path exists for),
// closes every connection registered before this call began (which unblocks any handleConn
// goroutine blocked reading from a peer that never sends and never disconnects), and waits up to
// serverCloseWait for those goroutines to finish. It is idempotent: a second call replays the
// first call's result rather than closing an already-closed listener again.
//
// Setting closing and closing every tracked connection happen inside one connsMu critical
// section — the same lock registerConn takes — which is what makes "has shutdown begun" and
// "what is currently tracked" one atomic fact rather than two that could disagree (N-2a).
// Closing the underlying listener itself is deliberately outside that critical section and
// bounded on its own (closeListenerBounded) — see that method's doc comment for why: it is a
// liveness concern for newly-arriving connections, not a safety concern for the registerConn
// race, so it does not need to be atomic with the tracking decision, and it must not be allowed
// to hold connsMu (or block Close itself) indefinitely if the platform's own Close hangs.
//
// Close does not unlink the POSIX socket path itself: net.UnixListener.Close already does that as
// part of closing the listener, and doing it again here — potentially up to serverCloseWait later
// — risks unlinking a *different*, newly-bound socket if a replacement daemon started in the
// window between this Close beginning and finishing.
func (s *server) Close() error {
	s.closeOnce.Do(func() {
		s.connsMu.Lock()
		s.closing = true
		for conn := range s.conns {
			_ = conn.Close()
		}
		s.connsMu.Unlock()

		s.closeErr = s.closeListenerBounded()
		s.waitForConns()
	})
	return s.closeErr
}

// closeListenerBounded closes the underlying listener, giving up after serverCloseWait rather
// than blocking Close forever if the platform's own Close hangs.
//
// This is not a hypothetical: it is a real, reproduced behaviour of go-winio v0.6.2's
// win32PipeListener.Close on Windows, found while building N-2a's regression stress test
// (TestServerConcurrentDialVsClose). win32PipeListener.Close sends on an unbuffered closeCh and
// waits synchronously for its internal listener goroutine to acknowledge shutdown by closing
// doneCh (pipe.go's win32PipeListener.Close/listenerRoutine). When that goroutine is between
// Accept cycles it acknowledges almost instantly, but when Close races a freshly-issued Accept
// that has not yet connected, the close signal is instead consumed by
// makeConnectedServerPipe's own inner select, which aborts the pending connect by closing the
// half-open pipe handle and then waits for the connectPipe goroutine's overlapped I/O to
// actually return before acknowledging shutdown — and in this specific race, that wait was
// observed exceeding 20s (untested how much further) in roughly one iteration in ten to twenty
// of a 100-iteration dial-vs-Close stress loop; the vast majority of iterations complete in
// well under a millisecond. Nothing in this package's own accept loop or registerConn logic
// causes it — it is entirely inside go-winio's own Accept/Close synchronization, and it existed,
// unnoticed, before this fix round.
//
// An orderly shutdown must have an upper bound even when a layer below this package does not
// honour one — the same trade-off waitForConns already makes for wg.Wait. Giving up here does
// not itself leak resources this package owns: it abandons waiting for the platform Close's own
// goroutine, not the daemon process, and s.closing is already true by the time this runs, so
// Serve's own loop still recognises the shutdown on its own the next time Accept returns for any
// reason.
func (s *server) closeListenerBounded() error {
	done := make(chan error, 1)
	go func() { done <- s.ln.Close() }()
	t := time.NewTimer(serverCloseWait)
	defer t.Stop()
	select {
	case err := <-done:
		return err
	case <-t.C:
		return nil
	}
}

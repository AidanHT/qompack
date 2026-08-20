//go:build windows

package ipc

import (
	"net"
	"time"

	winio "github.com/Microsoft/go-winio"
)

// dial opens the named pipe a.Path names, waiting at most timeout for it to become available.
//
// go-winio is on §2.5's closed runtime dependency list for exactly this call: the standard library
// has no named-pipe support, and the alternatives (a TCP loopback listener, an HTTP endpoint) are
// both network I/O and therefore forbidden by D10. A named pipe is a same-machine kernel object,
// ACL'd to the current user's SID by the Server that created it; nothing here reaches a host, a
// port or a network stack.
//
// dial is unexported and is wired into Client.Send by SP-05. SP-01 ships it real, together with
// Resolve, because §2.4 specifies both completely and a later subplan should inherit them rather
// than re-derive the endpoint naming.
func dial(a Addr, timeout time.Duration) (net.Conn, error) {
	return winio.DialPipe(a.Path, &timeout)
}

// dialBusyRetryQuantum is how far past the timeout it was given a dial can return on this
// platform, and the smallest connect budget that buys more than one attempt.
//
// ERROR_PIPE_BUSY is what CreateFile returns when the pipe NAME exists but every instance of it is
// already claimed — the state a listener is in between accepting one connection and creating the
// next instance. go-winio's tryDialPipe answers that with a hard-coded `time.Sleep(10 *
// time.Millisecond)` (go-winio@v0.6.2/pipe.go:227-229, "Wait 10 msec and try again") and re-checks
// the caller's deadline only at the top of the next iteration (same file, 208-211), so:
//
//   - a dial can return up to one quantum after its nominal timeout, because the sleep that
//     straddles the deadline still runs to completion before the deadline is next looked at; and
//   - a timeout below one quantum buys exactly one CreateFile attempt, since the first retry
//     already outlives it.
//
// It is declared here, beside the call it describes, rather than copied into whichever test has to
// bound a connect: a bound derived from this constant moves if the platform's dial ever changes,
// and a copy of "10ms" does not (internal/daemon/timing.go states the same rule for the daemon's
// own timing).
const dialBusyRetryQuantum = 10 * time.Millisecond

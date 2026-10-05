//go:build !windows

package ipc

import (
	"net"
	"time"
)

// dial connects to the Unix domain socket a.Path names, giving up after timeout.
//
// The network argument is the literal string "unix" and nothing else: AF_UNIX carries no packets
// and reaches no host, which is why D10's "no network I/O, ever" and this call coexist. It is
// written through a net.Dialer rather than as a bare net.Dial so the timeout the caller passed is
// actually honoured — Send runs on the hot path against an ACK deadline measured in single-digit
// milliseconds (§2.4), and a dial with no deadline would blow that budget on a stale socket file
// left behind by a dead daemon.
//
// dial is unexported and is wired into Client.Send by SP-05. SP-01 ships it real, together with
// Resolve, because §2.4 specifies both completely and a later subplan should inherit them rather
// than re-derive the endpoint naming.
func dial(a Addr, timeout time.Duration) (net.Conn, error) {
	d := net.Dialer{Timeout: timeout}
	return d.Dial("unix", a.Path)
}

// dialBusyRetryQuantum is the platform's counterpart to dial_windows.go's constant of the same
// name — see there for what it is for. It is zero here: Go makes one non-blocking connect(2) on an
// AF_UNIX socket, which either succeeds, the kernel queueing the connection for the listener's next
// accept, or fails at once. ENOENT means no socket file, and ECONNREFUSED a file nobody listens on.
// A listener whose queue is full fails it at once too, never waiting for the queue to drain: Linux
// returns EAGAIN for a non-blocking connect, and macOS refuses it (ECONNREFUSED). So a live daemon
// that has stopped accepting and has a full queue reads as no daemon, and a spawner that dials it
// starts a second daemon, which loses daemon.lock and exits (internal/daemon spawn.go). There is no
// busy-retry sleep between attempts to overshoot the caller's budget, because there are no repeated
// attempts.
const dialBusyRetryQuantum time.Duration = 0

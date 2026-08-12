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

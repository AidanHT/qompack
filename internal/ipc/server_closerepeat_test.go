package ipc

import (
	"net"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// repeatClosedListener is a listener whose first Close is slow: it returns only after a second Close
// has run, and then reports what a POSIX net.UnixListener reports to a Close that another Close beat
// to the descriptor, net.ErrClosed. Only Close is ever called on it; the embedded Listener is nil.
type repeatClosedListener struct {
	net.Listener

	mu     sync.Mutex
	calls  int
	second chan struct{}
}

func (l *repeatClosedListener) Close() error {
	l.mu.Lock()
	l.calls++
	n := l.calls
	l.mu.Unlock()
	switch n {
	case 1:
		<-l.second
		return net.ErrClosed
	case 2:
		close(l.second)
	}
	return nil
}

// TestCloseListenerBoundedTreatsItsOwnRepeatAsSuccess pins the interleaving macos-15-intel's test
// leg met under -race (ci.yml run 38478943482, TestServerConcurrentDialVsClose, iteration 91): the
// platform Close that closeListenerBounded called first had not returned within
// listenerCloseResignal, the repeat it then sent closed the listener, and the first call came back
// "use of closed network connection". Server.Close returned that as its error, and the daemon's Stop
// with it, for a listener this server had itself closed. Only closeListenerBounded closes the
// listener, so net.ErrClosed there is one of its own calls having won, which is success.
func TestCloseListenerBoundedTreatsItsOwnRepeatAsSuccess(t *testing.T) {
	ln := &repeatClosedListener{second: make(chan struct{})}
	s := &server{ln: ln}

	require.NoError(t, s.closeListenerBounded(),
		"a listener closed by closeListenerBounded's own repeat is a listener that closed")

	ln.mu.Lock()
	defer ln.mu.Unlock()
	require.GreaterOrEqual(t, ln.calls, 2, "the fixture must have taken the repeat path it exists for")
}

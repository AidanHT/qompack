//go:build windows

package ipc

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/logging"
)

// TestSendFailure_BusyPipeIsNotAccepted: a pipe whose every instance is claimed answers a dial
// with ERROR_PIPE_BUSY until the connect deadline runs out. That is a daemon that exists and did
// not accept in time, which is reported apart from no daemon at all.
//
// The listener here is never served: with no pending ConnectNamedPipe on its one instance, go-winio's
// dial finds the pipe busy and retries until the deadline, which is how a daemon that has stopped
// accepting looks to a client.
func TestSendFailure_BusyPipeIsNotAccepted(t *testing.T) {
	addr, err := Resolve(t.TempDir())
	require.NoError(t, err)
	ln, err := listen(addr, logging.Nop(), MaxLineBytes)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })

	log := &failureLog{}
	c, _ := newTestClient(t, addr, ClientOptions{OnFailure: log.record})
	_, err = c.Send(context.Background(), statusRequest(true), testDeadline)
	require.NoError(t, err)
	require.Equal(t, []SendFailure{SendNotAccepted}, log.all())
}

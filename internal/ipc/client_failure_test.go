package ipc

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sync"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/config"
)

// failureLog records every SendFailure a client reports through ClientOptions.OnFailure.
type failureLog struct {
	mu  sync.Mutex
	got []SendFailure
}

func (l *failureLog) record(f SendFailure) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.got = append(l.got, f)
}

func (l *failureLog) all() []SendFailure {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]SendFailure(nil), l.got...)
}

// statusRequest is a Reply request on an op every daemon routes. The failure report is a property
// of the transport, so any known op serves.
func statusRequest(reply bool) Request {
	return Request{Op: OpStatus, Session: "s-failure", TS: 1, Reply: reply}
}

// TestSendFailure_NothingListening: a dial that finds no listener is reported as such, once, and
// Send still spools and returns no error.
func TestSendFailure_NothingListening(t *testing.T) {
	addr, err := Resolve(t.TempDir())
	require.NoError(t, err)
	log := &failureLog{}
	c, spool := newTestClient(t, addr, ClientOptions{OnFailure: log.record})

	resp, err := c.Send(context.Background(), statusRequest(true), testDeadline)
	require.NoError(t, err)
	require.False(t, resp.OK)
	require.Equal(t, []SendFailure{SendNotListening}, log.all())
	require.Len(t, readLines(t, spool.Path()), 1, "the report changes nothing about the spool fallback")
}

// TestSendFailure_AnAnswerIsNoFailure: whatever the daemon answers, served or refused, the transport
// did not fail, so nothing is reported.
func TestSendFailure_AnAnswerIsNoFailure(t *testing.T) {
	for name, answer := range map[string]Response{
		"served":  {OK: true},
		"refused": {OK: false, Err: "not today"},
	} {
		t.Run(name, func(t *testing.T) {
			_, addr := newTestServer(t, func(context.Context, Request) Response { return answer })
			log := &failureLog{}
			c, _ := newTestClient(t, addr, ClientOptions{
				OnFailure: log.record, ConnectDeadline: answerDeadline, AckDeadline: answerDeadline,
			})

			resp, err := c.Send(context.Background(), statusRequest(true), answerDeadline)
			require.NoError(t, err)
			require.Equal(t, answer.OK, resp.OK)
			require.Equal(t, answer.Err, resp.Err)
			require.Empty(t, log.all())
		})
	}
}

// TestSendFailure_AcceptedButUnanswered: a listener that accepts and then says nothing is reported
// as unanswered, on both the reply path and the ACK path.
func TestSendFailure_AcceptedButUnanswered(t *testing.T) {
	for _, reply := range []bool{true, false} {
		t.Run(fmt.Sprintf("reply=%t", reply), func(t *testing.T) {
			addr, err := Resolve(t.TempDir())
			require.NoError(t, err)
			startMisbehavingListener(t, addr, "hangs")
			log := &failureLog{}
			c, _ := newTestClient(t, addr, ClientOptions{OnFailure: log.record, ConnectDeadline: answerDeadline})

			_, err = c.Send(context.Background(), statusRequest(reply), testDeadline)
			require.NoError(t, err)
			require.Equal(t, []SendFailure{SendUnanswered}, log.all())
		})
	}
}

// TestSendFailure_BrokenAnswer: a connection that closes without an answer, or answers with a byte
// that is neither ACK nor NAK, is a broken exchange rather than a missed deadline.
func TestSendFailure_BrokenAnswer(t *testing.T) {
	for _, tc := range []struct {
		behavior string
		reply    bool
	}{
		{"closes-immediately", true},
		{"bogus-byte", false},
	} {
		t.Run(tc.behavior, func(t *testing.T) {
			addr, err := Resolve(t.TempDir())
			require.NoError(t, err)
			startMisbehavingListener(t, addr, tc.behavior)
			log := &failureLog{}
			c, _ := newTestClient(t, addr, ClientOptions{
				OnFailure: log.record, ConnectDeadline: answerDeadline, AckDeadline: answerDeadline,
			})

			_, err = c.Send(context.Background(), statusRequest(tc.reply), answerDeadline)
			require.NoError(t, err)
			require.Equal(t, []SendFailure{SendBroken}, log.all())
		})
	}
}

// TestSendFailure_DaemonDisabled: a state that disables the daemon never dials, and says why.
func TestSendFailure_DaemonDisabled(t *testing.T) {
	addr, err := Resolve(t.TempDir())
	require.NoError(t, err)
	st := StateFromConfig(config.Defaults())
	st.DaemonEnabled = false
	log := &failureLog{}
	c, _ := newTestClient(t, addr, ClientOptions{State: st, OnFailure: log.record})

	_, err = c.Send(context.Background(), statusRequest(true), testDeadline)
	require.NoError(t, err)
	require.Equal(t, []SendFailure{SendDisabled}, log.all())
}

// TestSendFailure_NoObserverIsFine: OnFailure is optional.
func TestSendFailure_NoObserverIsFine(t *testing.T) {
	addr, err := Resolve(t.TempDir())
	require.NoError(t, err)
	c, _ := newTestClient(t, addr, ClientOptions{})
	require.NotPanics(t, func() { _, _ = c.Send(context.Background(), statusRequest(true), testDeadline) })
}

// timeoutErr is a dial error that says it timed out, as go-winio's ErrTimeout and a net.OpError
// over os.ErrDeadlineExceeded both do.
type timeoutErr struct{}

func (timeoutErr) Error() string   { return "i/o timeout" }
func (timeoutErr) Timeout() bool   { return true }
func (timeoutErr) Temporary() bool { return true }

// TestClassifyConnectFailure: a dial that ran out of time found something holding the address that
// did not accept; any other refusal means nothing is listening; a cancelled caller is neither.
func TestClassifyConnectFailure(t *testing.T) {
	live := context.Background()
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()

	for name, tc := range map[string]struct {
		ctx  context.Context
		err  error
		want SendFailure
	}{
		"winio timeout":        {live, timeoutErr{}, SendNotAccepted},
		"wrapped timeout":      {live, fmt.Errorf("dial: %w", timeoutErr{}), SendNotAccepted},
		"os deadline":          {live, os.ErrDeadlineExceeded, SendNotAccepted},
		"budget spent":         {live, context.DeadlineExceeded, SendNotAccepted},
		"no pipe or socket":    {live, &fs.PathError{Op: "open", Path: "p", Err: fs.ErrNotExist}, SendNotListening},
		"refused":              {live, syscall.ECONNREFUSED, SendNotListening},
		"anything else":        {live, errors.New("odd"), SendNotListening},
		"caller cancelled":     {cancelled, context.Canceled, SendBroken},
		"caller gone, timeout": {cancelled, timeoutErr{}, SendBroken},
	} {
		t.Run(name, func(t *testing.T) {
			require.Equal(t, tc.want, classifyConnectFailure(tc.ctx, tc.err))
		})
	}
}

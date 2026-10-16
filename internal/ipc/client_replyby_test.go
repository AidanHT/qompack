package ipc

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestSendReplyBy_EndsTheWaitAtTheInstant: a Reply request against a listener that accepts and never
// answers waits until ReplyBy, not for the whole Send deadline, and is reported unanswered.
func TestSendReplyBy_EndsTheWaitAtTheInstant(t *testing.T) {
	addr, err := Resolve(t.TempDir())
	require.NoError(t, err)
	startMisbehavingListener(t, addr, "hangs")
	log := &failureLog{}
	began := time.Now()
	c, _ := newTestClient(t, addr, ClientOptions{
		OnFailure: log.record, ConnectDeadline: answerDeadline, ReplyBy: began.Add(testDeadline),
	})

	_, err = c.Send(context.Background(), statusRequest(true), handlerSeenWait)
	require.NoError(t, err)
	require.Equal(t, []SendFailure{SendUnanswered}, log.all())
	require.Less(t, time.Since(began), handlerSeenWait, "ReplyBy, not the Send deadline, ended the wait")
}

// TestSendReplyBy_TheSendDeadlineStillBounds: a ReplyBy later than the Send deadline leaves the
// deadline in charge.
func TestSendReplyBy_TheSendDeadlineStillBounds(t *testing.T) {
	addr, err := Resolve(t.TempDir())
	require.NoError(t, err)
	startMisbehavingListener(t, addr, "hangs")
	log := &failureLog{}
	began := time.Now()
	c, _ := newTestClient(t, addr, ClientOptions{
		OnFailure: log.record, ConnectDeadline: answerDeadline, ReplyBy: began.Add(handlerSeenWait),
	})

	_, err = c.Send(context.Background(), statusRequest(true), testDeadline)
	require.NoError(t, err)
	require.Equal(t, []SendFailure{SendUnanswered}, log.all())
	require.Less(t, time.Since(began), handlerSeenWait, "the Send deadline ended the wait")
}

// TestSendReplyBy_AnAnswerInTimeIsServed: an answer that arrives before ReplyBy is the answer.
func TestSendReplyBy_AnAnswerInTimeIsServed(t *testing.T) {
	_, addr := newTestServer(t, func(context.Context, Request) Response { return Response{OK: true, Err: "here"} })
	log := &failureLog{}
	c, _ := newTestClient(t, addr, ClientOptions{
		OnFailure: log.record, ConnectDeadline: answerDeadline, AckDeadline: answerDeadline,
		ReplyBy: time.Now().Add(answerDeadline),
	})

	resp, err := c.Send(context.Background(), statusRequest(true), answerDeadline)
	require.NoError(t, err)
	require.True(t, resp.OK)
	require.Equal(t, "here", resp.Err)
	require.Empty(t, log.all())
}

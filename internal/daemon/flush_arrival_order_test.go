package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/paths"
)

// V6 close-out C1.2 review finding / C1.13: SessionEnd is not ordered after the session's own
// accepted arrivals. flushRoute runs registry.End, ingest.CloseSession (which only closes the WAL
// handle), svc.SessionEnd and only then Drain. The observer's SessionEnd closes the segment at the
// session's current turn and DELETES the session's in-memory state, so a tool the daemon had already
// accepted and leased, but no worker had dispatched yet, is published afterwards onto a fresh
// sessionState at turn 0: the index then holds turn 0 after turn 1 for one session. The e2e
// rehearsal showed it with one ingest worker and the ordering gate on (E1: the last tool of each
// turn at turn 0); it is independent of the gate's stranding (C1.1), which only widens the window.
//
// The property: a flush may finish the session only after every accepted leased arrival of that
// session has been dispatched or drained, and a caller that waits for its answer must still get it
// inside the bound the hook client used to wait.
//
// These arms send the flush as a Reply request, which asks for the session end's own answer. Since
// C1.15 the hook client sends it fire-and-forget and the daemon ends the session asynchronously
// (session_end.go); a Reply flush still waits for that end, so "the flush answered" still means "the
// session has ended" here, which is what the assertions below read.

// flushOrderReplyBound is the 15 s the hook client waited for a flush's answer before C1.15
// (cli.flushReplyDeadline then), restated because internal/daemon cannot import internal/cli. It is
// kept as the bound a Reply flush must answer within, the one this acceptance test was written
// against.
const flushOrderReplyBound = 15 * time.Second

// flushOrderTool is one leased observe.tool delivery: a Read of an in-project file, the shape the
// daemon admits itself when the client sent no capture decision.
func flushOrderTool(dd *daemon, root string, sess core.SessionID, nonce, id string) ipc.Request {
	return ipc.Request{
		Op: ipc.OpObserveTool, Session: sess, TS: core.NowMilli(dd.clk), Nonce: nonce,
		Event: &hookio.Event{
			HookEventName: "PostToolUse", SessionID: sess, CWD: root,
			ToolName: "Read", ToolUseID: core.ToolUseID(id),
			ToolInput:    json.RawMessage(fmt.Sprintf(`{"file_path":"src/%s.go"}`, id)),
			ToolResponse: json.RawMessage(fmt.Sprintf(`{"content":"package %s\n"}`, "flush")),
		},
	}
}

// flushOrderDaemon is the real observer and store behind a daemon that holds its delivery lock and
// the drainer Run installs, with no ingest worker started: each arm decides who dispatches what.
func flushOrderDaemon(t *testing.T) (*daemon, *Options, string) {
	t.Helper()
	root := t.TempDir()
	_, dd, o := wireTestDaemon(t, root, nil)
	lock := lockFor(t, dd, root)
	t.Cleanup(func() { _ = lock.Release() })
	t.Cleanup(func() {
		grace, cancel := context.WithTimeout(context.Background(), promptRecordWait)
		defer cancel()
		dd.stopPromptRecordings(grace)
	})
	dd.drain.Store(newDrainer(dd.drainConfig()))
	return dd, o, root
}

// flushOrderSessionTools reads the last index/sessions.jsonl record for sess, the counters the
// SessionEnd flush (store.Flush) appended. Zero when the session has no record.
func flushOrderSessionTools(t *testing.T, root string, sess core.SessionID) int {
	t.Helper()
	f, err := os.Open(paths.Long(filepath.Join(paths.Of(root).Index, "sessions.jsonl")))
	require.NoError(t, err, "SessionEnd writes the session index")
	defer func() { _ = f.Close() }()
	n := 0
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var rec struct {
			S        core.SessionID `json:"s"`
			ToolUses int            `json:"tooluses"`
		}
		require.NoError(t, json.Unmarshal(sc.Bytes(), &rec))
		if rec.S == sess {
			n = rec.ToolUses
		}
	}
	require.NoError(t, sc.Err())
	return n
}

// flushOrderRun issues the SessionEnd flush on its own goroutine and requires an OK answer inside
// flushOrderReplyBound, the time the hook client waits for one.
func flushOrderRun(t *testing.T, dd *daemon, sess core.SessionID) {
	t.Helper()
	done := make(chan ipc.Response, 1)
	go func() { done <- dd.dispatchOp(context.Background(), liveWALSessionEnd(dd, sess)) }()
	select {
	case resp := <-done:
		require.True(t, resp.OK, "the flush must finish the session: %s", resp.Err)
	case <-time.After(flushOrderReplyBound):
		t.Fatalf("the flush did not answer within the hook client's %s reply deadline", flushOrderReplyBound)
	}
}

// flushOrderRequireOrdered is the property: the tool that was still queued when the flush arrived
// is recorded in the same turn as the tool published before it, and the session record SessionEnd
// wrote counts it — SessionEnd ran after it, not before.
func flushOrderRequireOrdered(t *testing.T, dd *daemon, o *Options, root string, sess core.SessionID,
	published, queued core.ToolUseID, wantTools int,
) {
	t.Helper()
	ctx := context.Background()
	a, err := o.Store.ToolUse(ctx, published)
	require.NoError(t, err, "the published tool has an index record")
	b, err := o.Store.ToolUse(ctx, queued)
	require.NoError(t, err, "the queued tool was accepted and leased, so it must be recorded")
	require.Positive(t, a.Turn, "the prompt before the tools moved the session past turn 0")
	require.Equal(t, a.Turn, b.Turn,
		"tool %s was accepted after %s in the same turn; it reports turn %d after turn %d, so SessionEnd "+
			"reset the session before the accepted arrival was published (turns are monotone)",
		queued, published, b.Turn, a.Turn)
	require.Equal(t, wantTools, flushOrderSessionTools(t, root, sess),
		"the session record SessionEnd wrote must count every accepted arrival of the session")
	require.Zero(t, dd.m.Counter(counterDrainFileError).Value(), "the flush drain met no delivery in progress")
}

// TestFlush_SessionEndIsOrderedAfterAcceptedArrivals pins that property in the two shapes the
// daemon produces: a tool whose job is still on the ring because no worker has reached it, and the
// same with one worker busy on another session's delivery — the one-worker e2e run (E1), where the
// worker lags the hooks. Both are deterministic: nothing dispatches the queued tool before the
// flush unless the flush itself makes that happen.
func TestFlush_SessionEndIsOrderedAfterAcceptedArrivals(t *testing.T) {
	const sess core.SessionID = "sess-flush-order"

	// Session start, one prompt and one tool, each published before the next arrives; then a
	// second tool of the same turn accepted and leased but left queued.
	begin := func(t *testing.T, dd *daemon, root string, publish func()) (core.ToolUseID, core.ToolUseID) {
		t.Helper()
		ctx := context.Background()
		require.True(t, dd.dispatchOp(ctx, liveWALSessionStart(dd, sess)).OK)
		require.True(t, dd.dispatchOp(ctx, spD3Prompt(dd, root, sess, orderNonce(0), "flush order prompt")).OK)
		publish()
		first := flushOrderTool(dd, root, sess, orderNonce(1), "toolu_flush_order_a")
		require.True(t, dd.dispatchOp(ctx, first).OK)
		publish()
		second := flushOrderTool(dd, root, sess, orderNonce(2), "toolu_flush_order_b")
		require.True(t, dd.dispatchOp(ctx, second).OK)
		return first.Event.ToolUseID, second.Event.ToolUseID
	}

	t.Run("queued job no worker has reached", func(t *testing.T) {
		dd, o, root := flushOrderDaemon(t)
		published, queued := begin(t, dd, root, func() { drainRing(t, dd) })
		require.Len(t, dd.ing.ring, 1, "the second tool is accepted and waiting for a worker")

		flushOrderRun(t, dd, sess)
		drainRing(t, dd) // the worker reaches the queued job only now
		flushOrderRequireOrdered(t, dd, o, root, sess, published, queued, 3)
	})

	t.Run("one worker busy on another session", func(t *testing.T) {
		dd, o, root := flushOrderDaemon(t)

		// One worker, parked in another session's handler until released, so the tool it would
		// otherwise reach stays on the ring through the flush. The blocker is a ring-only job with
		// no WAL line and no lease, and its handler persists nothing: the flush's drain reads the
		// whole spool, and an in-flight delivery of another session there would fail the flush for
		// a reason that is not this property.
		const other core.SessionID = "sess-flush-order-other"
		release := make(chan struct{})
		var once sync.Once
		open := func() { once.Do(func() { close(release) }) }
		parked := make(chan struct{}, 1)
		run := func(ctx context.Context, req ipc.Request) ipc.Response {
			if req.Session == other {
				parked <- struct{}{}
				<-release
				return ipc.Response{OK: true}
			}
			return dd.runIngested(ctx, req)
		}

		// Everything before the queued tool is published by hand first, so that the worker's only
		// job ahead of it is the blocker; then the worker starts and parks in the blocker.
		published, queued := begin(t, dd, root, func() { drainRing(t, dd) })
		require.Len(t, dd.ing.ring, 1)
		pending := <-dd.ing.ring
		dd.ing.ring <- job{
			req:  flushOrderTool(dd, root, other, "", "toolu_flush_order_other"),
			recv: core.NowMilli(dd.clk),
			key:  core.HashBytes("qompack.test.flush-order-blocker", []byte(other)),
		}
		dd.ing.ring <- pending // the blocker is ahead of the queued tool, as when the worker lags

		ctx, cancel := context.WithCancel(context.Background())
		dd.ing.Start(ctx, 1, run)
		t.Cleanup(func() {
			open()
			cancel()
			dd.ing.Wait()
		})
		select {
		case <-parked:
		case <-time.After(flushOrderReplyBound):
			t.Fatal("the worker never reached the other session's delivery")
		}
		require.Len(t, dd.ing.ring, 1, "the queued tool waits behind the busy worker")

		flushOrderRun(t, dd, sess)
		open() // the worker gets to the queued job only after the flush answered
		require.Eventually(t, func() bool { return len(dd.ing.ring) == 0 }, flushOrderReplyBound, 10*time.Millisecond)
		flushOrderRequireOrdered(t, dd, o, root, sess, published, queued, 3)
	})
}

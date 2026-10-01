package daemon

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/observer"
	"github.com/qompack/qompack/internal/store"
)

// promptRecordWait bounds how long a test waits on a channel the recording goroutine owns. It is a
// hang guard only, never a budget under test: every assertion below is ordered by channels.
const promptRecordWait = 10 * time.Second

// stopJoinProbe is how long a test watches a Stop that must NOT return yet — its capture is still
// held open by the test. A Stop that joins cannot return during the probe however loaded the
// machine is, because only the test can let the capture go; the probe's length matters only to
// the negative direction, giving a Stop that skipped the join time to finish its own tail (ingest
// close, sketch save, metrics persist, state removal) and show itself.
const stopJoinProbe = 200 * time.Millisecond

// observerPromptPutErr is the soft-failure counter observer.onUserPrompt drops a lost verbatim
// capture into (observer.go counterErrPrefix + prompt.go stagePromptPut). Respelled because both
// halves are unexported there.
const observerPromptPutErr = "observer.err.prompt.put"

// heldPromptSeam is an ObservePrompt seam that stands in for observer.OnUserPrompt held past the
// reply deadline — the session lock taken by ingest workers draining a tool backlog, or a slow disk.
// It signals entry, blocks until released, and then reports ctx.Err(): the exact check
// store.PutBytes and store.RecordToolUse make first, whose failure observer.onUserPrompt
// soft-drops into observer.err.prompt.put.
//
// If its context is cancelled instead, it signals cancelled and then holds on for as long as
// afterCancel stays open before it reports — a capture still unwinding when shutdown cancels it.
// afterCancel starts closed, so a seam no test holds reports at once.
type heldPromptSeam struct {
	entered     chan struct{}
	release     chan struct{}
	cancelled   chan struct{}
	afterCancel chan struct{}
	recorded    chan error

	mu    sync.Mutex
	calls int
}

func newHeldPromptSeam() *heldPromptSeam {
	afterCancel := make(chan struct{})
	close(afterCancel)
	return &heldPromptSeam{
		entered:     make(chan struct{}, 1),
		release:     make(chan struct{}),
		cancelled:   make(chan struct{}, 1),
		afterCancel: afterCancel,
		recorded:    make(chan error, 1),
	}
}

func (s *heldPromptSeam) observe(ctx context.Context, _ hookio.Event) (hookio.Output, error) {
	s.mu.Lock()
	s.calls++
	s.mu.Unlock()
	s.entered <- struct{}{}
	select {
	case <-s.release:
	case <-ctx.Done():
		s.cancelled <- struct{}{}
		<-s.afterCancel
	}
	s.recorded <- ctx.Err()
	return hookio.Output{HookSpecificOutput: &hookio.HSO{
		HookEventName: "UserPromptSubmit", AdditionalContext: "late thrash warning",
	}}, nil
}

func (s *heldPromptSeam) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func newPromptSeamDaemon(t *testing.T, seam *heldPromptSeam) *daemon {
	t.Helper()
	o := Options{ProjectRoot: t.TempDir(), Cfg: testConfig(), Log: logging.Nop()}
	o.Bind(func(s *Services) { s.ObservePrompt = seam.observe })
	d, err := New(o)
	require.NoError(t, err)
	dd, ok := d.(*daemon)
	require.True(t, ok)
	return dd
}

func promptRequest(dd *daemon, sess core.SessionID) ipc.Request {
	ev := &hookio.Event{HookEventName: "UserPromptSubmit", SessionID: sess, Prompt: "fix the refresh handler"}
	return ipc.Request{Op: ipc.OpObservePrompt, Session: sess, Reply: true, Event: ev, TS: core.NowMilli(dd.clk)}
}

// awaitSignal waits on a channel the recording goroutine owns, failing the test with what never
// happened rather than hanging it.
func awaitSignal(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(promptRecordWait):
		require.FailNow(t, what)
	}
}

// TestObservePrompt_RecordingOutlivesTheReplyDeadline is the G2.3 regression behind the V5
// close-out's lost first prompt: promptReplyDeadline bounds how long the HOOK waits for its reply,
// and must never cancel the verbatim capture itself. The seam is held past the deadline and then
// released; the recording must still see a live context, and its late Output must not reach the
// reply that already went out empty.
func TestObservePrompt_RecordingOutlivesTheReplyDeadline(t *testing.T) {
	t.Parallel()

	seam := newHeldPromptSeam()
	dd := newPromptSeamDaemon(t, seam)
	t.Cleanup(func() { _ = dd.Stop(context.Background()) })

	req := promptRequest(dd, "sess-late")
	resp := dd.dispatchOp(context.Background(), req)

	// The reply went out on the deadline, empty: a prompt is never blocked on the daemon.
	require.True(t, resp.OK)
	require.NotNil(t, resp.Output)
	require.Nil(t, resp.Output.HookSpecificOutput, "an overrun reply must still be hookio.Empty()")

	awaitSignal(t, seam.entered, "the ObservePrompt seam was never called")
	// Released only now, after dispatchOp has returned and every deferred cancel on the reply path
	// has run — the instant the old code's recording context was already dead.
	close(seam.release)

	select {
	case err := <-seam.recorded:
		require.NoError(t, err, "the verbatim capture must not run under the reply deadline's context")
	case <-time.After(promptRecordWait):
		require.FailNow(t, "the recording never finished")
	}
	require.Equal(t, 1, seam.callCount(), "the reply path calls the seam once, for the warning")
	require.Equal(t, int64(1), dd.m.Counter(counterPromptReplyLate).Value(),
		"a reply that went out before its capture finished must be counted")

	// SP08-D3 (Option A): the authoritative verbatim capture is the WORKER/REPLAY's, so replaying
	// this WAL line through runIngested DOES call the seam (that is the fix — a replayed prompt is
	// captured, not lost). "No double recording" is therefore guaranteed by the observation-identity
	// join (observationRecord), not by never re-calling the seam; it is pinned end to end, with a real
	// store and sidecar, by TestCarriedDefect_SP08D3_ReplayedPromptIsCapturedAtTurnZero. This held-seam
	// rig has no sidecar, so it cannot express that convergence and no longer asserts the obsolete
	// sentinel-only contract here.
}

// TestObservePrompt_CancelledRequestIsNotAnOverrun: a request whose own context is cancelled —
// what Stop does to every in-flight request when it cancels the serving context — ends the reply
// wait at once, but it is not an overrun, so it is not counted as late; and the capture that
// request started still runs on a live context of its own.
func TestObservePrompt_CancelledRequestIsNotAnOverrun(t *testing.T) {
	t.Parallel()

	seam := newHeldPromptSeam()
	dd := newPromptSeamDaemon(t, seam)
	t.Cleanup(func() { _ = dd.Stop(context.Background()) })

	// Cancelled before the call, so the reply wait sees context.Canceled and never the deadline,
	// however long the machine takes to get there.
	reqCtx, cancel := context.WithCancel(context.Background())
	cancel()
	resp := dd.dispatchOp(reqCtx, promptRequest(dd, "sess-cancelled-request"))
	require.True(t, resp.OK)
	require.NotNil(t, resp.Output)
	require.Nil(t, resp.Output.HookSpecificOutput, "a cancelled request's reply must be hookio.Empty()")

	awaitSignal(t, seam.entered, "a cancelled request must still start its verbatim capture")
	close(seam.release)
	select {
	case err := <-seam.recorded:
		require.NoError(t, err, "the request's cancellation must not reach the verbatim capture")
	case <-time.After(promptRecordWait):
		require.FailNow(t, "the recording never finished")
	}
	require.Equal(t, int64(0), dd.m.Counter(counterPromptReplyLate).Value(),
		"a reply ended by its request's cancellation is not a reply that ran out its deadline")
}

// TestObservePrompt_PanickingSeamIsRecovered: the capture goroutine outlives the request, so
// callHandler's recover cannot reach it. A panic in the seam must be recovered on that goroutine
// and counted as callHandler counts one, the reply must still be hookio.Empty(), the panic must
// not be miscounted as a late reply, and the goroutine must still release its slot in Stop's join.
func TestObservePrompt_PanickingSeamIsRecovered(t *testing.T) {
	t.Parallel()

	o := Options{ProjectRoot: t.TempDir(), Cfg: testConfig(), Log: logging.Nop()}
	o.Bind(func(s *Services) {
		s.ObservePrompt = func(context.Context, hookio.Event) (hookio.Output, error) {
			panic("observe.prompt seam boom")
		}
	})
	d, err := New(o)
	require.NoError(t, err)
	dd, ok := d.(*daemon)
	require.True(t, ok)
	t.Cleanup(func() { _ = dd.ing.Close() })

	// No hook stamp: the reply gets the whole deadline from the route (promptReplyBudget), so the
	// late counter below does not depend on how long this machine's WAL fsync in ingest.Accept takes.
	// A reply whose stamped budget was already spent is counted late whatever the seam does; that is
	// TestPromptWarning_SlowDurableAcceptIsLateForTheClient's subject, not this row's.
	req := promptRequest(dd, "sess-panic")
	req.TS = 0
	var resp ipc.Response
	require.NotPanics(t, func() { resp = dd.dispatchOp(context.Background(), req) })
	require.True(t, resp.OK)
	require.NotNil(t, resp.Output)
	require.Nil(t, resp.Output.HookSpecificOutput, "a panicked capture's reply must be hookio.Empty()")

	// Deterministic: Wait returns only once the recovered goroutine has called Done. A recovery
	// that skipped Done would leave Stop waiting out the whole drain window on every shutdown.
	joined := make(chan struct{})
	go func() {
		dd.promptWG.Wait()
		close(joined)
	}()
	awaitSignal(t, joined, "the panicked capture never released its slot in Stop's join")

	require.Equal(t, int64(1), dd.m.Counter(counterHandlerPanic).Value(),
		"a panic in the capture goroutine must be counted exactly as callHandler counts one")
	require.Equal(t, int64(0), dd.m.Counter(counterPromptReplyLate).Value(),
		"a panicking seam answers the wait at once; it is not a reply that ran out its deadline")

	stopped := make(chan error, 1)
	go func() { stopped <- dd.Stop(context.Background()) }()
	select {
	case err := <-stopped:
		require.NoError(t, err)
	case <-time.After(promptRecordWait):
		require.FailNow(t, "Stop never returned after a panicked capture")
	}
}

// TestObservePrompt_StopJoinsAnInFlightRecording: a capture still running when Stop begins is given
// the drain's window to finish on its own, with its context intact, and Stop does not return until
// it has — no goroutine outlives the daemon, and nothing it writes races the store's close.
func TestObservePrompt_StopJoinsAnInFlightRecording(t *testing.T) {
	t.Parallel()

	seam := newHeldPromptSeam()
	dd := newPromptSeamDaemon(t, seam)
	// The post-Stop dispatch below still WAL-appends (the route's Accept runs before the capture
	// gate), reopening a handle Stop's own ingest close has already passed.
	t.Cleanup(func() { _ = dd.ing.Close() })

	resp := dd.dispatchOp(context.Background(), promptRequest(dd, "sess-stop-join"))
	require.True(t, resp.OK)
	awaitSignal(t, seam.entered, "the ObservePrompt seam was never called")

	stopped := make(chan error, 1)
	go func() { stopped <- dd.Stop(context.Background()) }()

	// Stop has closed the gate, i.e. is inside the join...
	require.Eventually(t, func() bool {
		dd.promptMu.Lock()
		defer dd.promptMu.Unlock()
		return dd.promptClosed
	}, promptRecordWait, time.Millisecond)
	// ...and must stay there while the capture is held. Only the test can release it, so a Stop
	// that joins cannot come back during the probe.
	select {
	case <-stopped:
		require.FailNow(t, "Stop returned while its in-flight capture was still running")
	case <-time.After(stopJoinProbe):
	}
	close(seam.release)

	select {
	case err := <-stopped:
		require.NoError(t, err)
	case <-time.After(promptRecordWait):
		require.FailNow(t, "Stop never returned")
	}
	// Buffered, and sent before the seam returned: present now only if Stop waited for it.
	select {
	case err := <-seam.recorded:
		require.NoError(t, err, "a capture that finishes inside the grace window must not be cancelled")
	default:
		require.FailNow(t, "Stop returned before the in-flight capture had finished")
	}

	// The gate stays shut: a prompt arriving after Stop starts no capture that could outlive it,
	// and the capture it loses is counted, not only logged.
	resp = dd.dispatchOp(context.Background(), promptRequest(dd, "sess-after-stop"))
	require.True(t, resp.OK)
	require.NotNil(t, resp.Output)
	require.Nil(t, resp.Output.HookSpecificOutput)
	require.Equal(t, 1, seam.callCount(), "no capture may start once Stop has joined the captures")
	require.Equal(t, int64(1), dd.m.Counter(counterPromptCaptureRefused).Value(),
		"a capture refused during shutdown is a lost G2.3 capture and must be countable")
}

// TestObservePrompt_StopCancelsARecordingThatOutlivesItsGrace: shutdown still governs the capture.
// Stop is handed a context that is already done, so the grace window is already spent; the capture
// that is still held must then see its context cancelled, and Stop must still JOIN it: the seam
// keeps running after the cancellation until the test lets it go, and Stop may not return before.
func TestObservePrompt_StopCancelsARecordingThatOutlivesItsGrace(t *testing.T) {
	t.Parallel()

	seam := newHeldPromptSeam()
	hold := make(chan struct{})
	seam.afterCancel = hold
	letGo := sync.OnceFunc(func() { close(hold) })
	t.Cleanup(letGo)
	dd := newPromptSeamDaemon(t, seam)
	// Raised so the test never races the post-cancel abandonment bound: the capture is held after
	// its cancellation for as long as the probe below takes, and only a real join may keep Stop
	// waiting that long. Production keeps promptReplyDeadline (New); nothing here lowers it.
	dd.promptAbandonAfter = promptRecordWait

	resp := dd.dispatchOp(context.Background(), promptRequest(dd, "sess-stop-cancel"))
	require.True(t, resp.OK)
	awaitSignal(t, seam.entered, "the ObservePrompt seam was never called")

	spent, cancel := context.WithCancel(context.Background())
	cancel()
	stopped := make(chan error, 1)
	go func() { stopped <- dd.Stop(spent) }()

	awaitSignal(t, seam.cancelled, "past its grace, a capture must be cancelled by shutdown")
	select {
	case <-stopped:
		require.FailNow(t, "Stop returned while the cancelled capture was still running: it did not join it")
	case <-time.After(stopJoinProbe):
	}
	letGo()

	select {
	case err := <-stopped:
		require.NoError(t, err)
	case <-time.After(promptRecordWait):
		require.FailNow(t, "Stop never returned")
	}
	select {
	case err := <-seam.recorded:
		require.ErrorIs(t, err, context.Canceled, "past its grace, a capture is cancelled by shutdown")
	default:
		require.FailNow(t, "Stop returned without joining the cancelled capture")
	}
}

// TestObservePrompt_StopAbandonsACaptureThatIgnoresCancellation pins the only bound Stop has on
// a capture that never answers its cancellation — the promptAbandonAfter timer
// stopPromptRecordings starts once it has cancelled what outlived its grace. That is the common
// production case, not an exotic one: observer.onUserPrompt parked in the session lock's Lock,
// which no context reaches. The seam here is cancelled and carries on regardless, held by the test
// until cleanup, so nothing but that timer can end Stop's wait. Every other test in this file lets
// its capture go or raises the bound out of reach, and a timer of an hour passed them all.
func TestObservePrompt_StopAbandonsACaptureThatIgnoresCancellation(t *testing.T) {
	t.Parallel()

	// Lowered from production's promptReplyDeadline only to keep the test quick, and distinct from
	// it, so the bound the Loud line reports is provably the field's.
	const bound = 50 * time.Millisecond
	// stopPromptRecordings Louds this as an inline literal, so it is respelled here.
	const abandonLoud = "daemon: stop: a verbatim prompt capture ignored cancellation; abandoning it"

	seam := newHeldPromptSeam()
	// Closed only in cleanup: the capture answers its cancellation by carrying on.
	hold := make(chan struct{})
	seam.afterCancel = hold
	logs := newRecordingLogger()
	o := Options{ProjectRoot: t.TempDir(), Cfg: testConfig(), Log: logs}
	o.Bind(func(s *Services) { s.ObservePrompt = seam.observe })
	d, err := New(o)
	require.NoError(t, err)
	dd, ok := d.(*daemon)
	require.True(t, ok)
	dd.promptAbandonAfter = bound
	// Registered after t.TempDir, so it runs before the project root is removed. It lets the capture
	// go whichever way it is waiting and calls Stop, a sync.Once: that returns only once the Stop
	// under test has run its whole tail, or runs one if the test failed before starting it. Then it
	// joins the abandoned capture, so no goroutine this test started outlives it.
	t.Cleanup(func() {
		close(seam.release)
		close(hold)
		_ = dd.Stop(context.Background())
		joined := make(chan struct{})
		go func() {
			dd.promptWG.Wait()
			close(joined)
		}()
		awaitSignal(t, joined, "the abandoned capture never finished once the test let it go")
	})

	resp := dd.dispatchOp(context.Background(), promptRequest(dd, "sess-stop-abandon"))
	require.True(t, resp.OK)
	awaitSignal(t, seam.entered, "the ObservePrompt seam was never called")

	// Already done, so the grace is spent before Stop begins: all that stands between Stop and its
	// return is the abandonment bound.
	spent, cancel := context.WithCancel(context.Background())
	cancel()
	stopped := make(chan error, 1)
	go func() { stopped <- dd.Stop(spent) }()

	awaitSignal(t, seam.cancelled, "past its grace, the capture must be cancelled first")
	select {
	case err := <-stopped:
		require.NoError(t, err)
	case <-time.After(promptRecordWait):
		require.FailNow(t, "Stop never returned: a capture ignoring its cancellation wedged the shutdown")
	}
	// Sent only once the test lets the seam go, which it has not yet done: Stop came back with the
	// capture still running, which is what abandoning it means.
	select {
	case <-seam.recorded:
		require.FailNow(t, "the capture finished before Stop returned, so nothing was abandoned")
	default:
	}

	// Never silent (§12): the abandonment is Loud'd once, naming the bound that ended the wait.
	var abandoned []logEntry
	for _, e := range logs.entries(logLoud) {
		if e.Msg == abandonLoud {
			abandoned = append(abandoned, e)
		}
	}
	require.Len(t, abandoned, 1,
		"an abandoned capture must be Loud'd exactly once; Loud lines: %q", logs.msgs(logLoud))
	var reported any
	for i := 0; i+1 < len(abandoned[0].KV); i += 2 {
		if abandoned[0].KV[i] == "bound" {
			reported = abandoned[0].KV[i+1]
		}
	}
	require.Equal(t, bound.String(), reported,
		"the abandonment line must name the bound that ended the wait: %v", abandoned[0].KV)
}

// gatedToolStore is a real store whose first tool Put blocks until released. observer.onToolUse
// takes the session lock before that Put (tooluse.go), so while it is held the ingest job holds
// the observer's session lock — the production overrun, a tool backlog in front of a prompt,
// reproduced with a gate instead of a sleep. The verbatim prompt Put is never gated.
type gatedToolStore struct {
	store.Store

	tool    string
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (s *gatedToolStore) PutBytes(ctx context.Context, b []byte, o store.PutOptions) (store.PutResult, error) {
	if o.Tool == s.tool {
		s.once.Do(func() {
			s.entered <- struct{}{}
			<-s.release
		})
	}
	return s.Store.PutBytes(ctx, b, o)
}

// TestObservePrompt_RealObserverCaptureLandsBehindAHeldSessionLock runs the V5 close-out's failure
// through the real observer and the real store, not a fake seam: an ingest job holds the session
// lock past promptReplyDeadline, so the prompt's reply goes out empty and late, and the verbatim
// capture must still land once the lock frees — its index record under VerbatimPromptID at turn 0,
// and nothing soft-dropped into observer.err.prompt.put. Before the fix the capture reached
// store.PutBytes on the reply's dead context and was lost, which is the rehydrator's "L0 verbatim
// capture unavailable; using the checkpoint copy".
func TestObservePrompt_RealObserverCaptureLandsBehindAHeldSessionLock(t *testing.T) {
	root := t.TempDir()
	backing, err := store.Open(root, testConfig(), store.Deps{})
	require.NoError(t, err)
	storeOwned := true
	t.Cleanup(func() {
		if storeOwned {
			_ = backing.Close()
		}
	})
	// The observer files a tool's Put under its DISPLAY name, not the host's tool_name.
	gated := &gatedToolStore{
		Store: backing, tool: observer.NormalizeToolName("Read"),
		entered: make(chan struct{}, 1), release: make(chan struct{}),
	}

	_, dd, _ := wireTestDaemon(t, root, func(o *Options) { o.Store = gated })
	storeOwned = false // wireTestDaemon now owns the supplied store's lifetime
	// Cleanups run last-registered first: release the gate, then join the capture, and only then
	// (wireTestDaemon's own cleanups) close the ingest handles and the store the capture writes to.
	t.Cleanup(func() {
		grace, cancel := context.WithTimeout(context.Background(), promptRecordWait)
		defer cancel()
		dd.stopPromptRecordings(grace)
	})
	letGo := sync.OnceFunc(func() { close(gated.release) })
	t.Cleanup(letGo)

	const sess core.SessionID = "sess-real-capture"
	toolReq := ipc.Request{
		Op: ipc.OpObserveTool, Session: sess, TS: core.NowMilli(dd.clk),
		Event: &hookio.Event{
			HookEventName: "PostToolUse", SessionID: sess, CWD: root, ToolName: "Read",
			ToolUseID: "toolu_real_capture_1", ToolInput: json.RawMessage(`{"file_path":"src/a.go"}`),
			ToolResponse: json.RawMessage(`{"content":"package a\n"}`),
		},
	}
	toolDone := make(chan ipc.Response, 1)
	go func() { toolDone <- dd.runIngested(context.Background(), toolReq) }()
	awaitSignal(t, gated.entered, "the tool observation never reached the store")

	// The ingest job now holds the session lock. The prompt's capture queues behind it, so the
	// reply can only go out on the deadline, empty.
	resp := dd.dispatchOp(context.Background(), promptRequest(dd, sess))
	require.True(t, resp.OK)
	require.NotNil(t, resp.Output)
	require.Nil(t, resp.Output.HookSpecificOutput, "an overrun reply must still be hookio.Empty()")
	require.Equal(t, int64(1), dd.m.Counter(counterPromptReplyLate).Value(),
		"the capture was held behind the session lock past the deadline")

	// The backlog clears only now, after the reply has gone and its context is long dead.
	letGo()
	select {
	case r := <-toolDone:
		require.True(t, r.OK, "the gated tool observation must still publish: %+v", r)
	case <-time.After(promptRecordWait):
		require.FailNow(t, "the gated tool observation never finished")
	}

	// SP08-D3 (Option A): the reply path is the WARNING only; the authoritative verbatim capture is
	// the worker's. The prompt's WAL line queued behind the held session lock is captured when the
	// ingest ring drains — which is exactly the "lands behind a held session lock" this test pins,
	// now on the worker rather than the reply goroutine.
	drainRing(t, dd)

	// A tool use does not advance the turn (only a prompt and a Stop do), so the prompt is turn 0.
	id := observer.VerbatimPromptID(sess, 0)
	var rec store.ToolUseRecord
	require.Eventually(t, func() bool {
		r, lookupErr := gated.ToolUse(context.Background(), id)
		if lookupErr != nil {
			return false
		}
		rec = r
		return true
	}, promptRecordWait, 10*time.Millisecond, "the late verbatim capture never landed: no %s index record", id)
	require.Equal(t, core.TurnIndex(0), rec.Turn)
	require.Equal(t, "UserPromptSubmit", rec.Tool)
	require.NotZero(t, rec.Root, "the index record must name the stored prompt bytes")
	require.Equal(t, int64(0), dd.m.Counter(observerPromptPutErr).Value(),
		"a capture held past the reply deadline must not be soft-dropped into %s", observerPromptPutErr)
}

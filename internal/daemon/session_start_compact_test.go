package daemon

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/contract"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/dag"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/logging"
)

// C1.16: a SessionStart(source=compact) must answer reliably fast, and never with a silent {}.
// session_start_compact.go says what the route does and why; these rows pin it.

// compactTestBound bounds every wait in this file. It is headroom for a loaded machine, not a
// latency expectation, and it is below compactAnswerBudget's production value (5 s), so a row that
// asserts "answered within compactTestBound" with the production budget in force cannot be
// satisfied by the deferred-note path.
const compactTestBound = 3 * time.Second

// gatedGraph is the DAG with a Flush that can be held open. The observer flushes the graph inside
// its per-session lock on every main-agent Stop (observer/stop.go mainAgentStop), so holding one
// Flush open holds that session's lock exactly as a slow same-session ingest event does in
// production: a worker writing a tool result's chunks under an fsync-heavy load.
type gatedGraph struct {
	dag.Graph

	mu      sync.Mutex
	armed   bool
	entered chan struct{}
	release chan struct{}
}

func newGatedGraph(g dag.Graph) *gatedGraph {
	return &gatedGraph{Graph: g, entered: make(chan struct{}), release: make(chan struct{})}
}

// arm makes the next Flush block until open is called.
func (g *gatedGraph) arm() {
	g.mu.Lock()
	g.armed = true
	g.mu.Unlock()
}

// open releases a held Flush, once.
func (g *gatedGraph) open() {
	g.mu.Lock()
	defer g.mu.Unlock()
	select {
	case <-g.release:
	default:
		close(g.release)
	}
}

func (g *gatedGraph) Flush(ctx context.Context) error {
	g.mu.Lock()
	armed := g.armed
	g.armed = false
	g.mu.Unlock()
	if armed {
		close(g.entered)
		<-g.release
	}
	return g.Graph.Flush(ctx)
}

// compactRequest is the session.start request a host's SessionStart(source=compact) becomes.
func compactRequest(root string, sess core.SessionID) ipc.Request {
	ev := &hookio.Event{HookEventName: "SessionStart", SessionID: sess, CWD: root, Source: "compact"}
	return ipc.Request{Op: ipc.OpSessionStart, Session: sess, Reply: true, Event: ev}
}

// dispatchWithin runs dd.dispatchOp(req) and returns its response, or fails the test with msg if
// it has not answered within bound. The call is left running on failure; the caller's cleanup
// releases whatever holds it.
func dispatchWithin(t *testing.T, dd *daemon, req ipc.Request, bound time.Duration, msg string) (ipc.Response, time.Duration) {
	t.Helper()
	type result struct {
		resp ipc.Response
		took time.Duration
	}
	ch := make(chan result, 1)
	start := time.Now()
	go func() {
		resp := dd.dispatchOp(context.Background(), req)
		ch <- result{resp, time.Since(start)}
	}()
	select {
	case r := <-ch:
		return r.resp, r.took
	case <-time.After(bound):
		require.FailNow(t, msg, "no answer within %s", bound)
		return ipc.Response{}, 0
	}
}

// joinReplyWork waits for every goroutine startReplyWork launched, as Stop does, with no clock: the
// grace never ends, so the join never cancels the work (promptCancel) before it has finished on its
// own. The rows go on to assert what that work recorded (drops, histograms, the sentinel), and a grace
// a stalled host outlasted turned them red on a cancelled record (wave 22). A hang is left to go test
// -timeout. t is kept for the call sites' symmetry with the other join helpers.
func joinReplyWork(t *testing.T, dd *daemon) {
	t.Helper()
	dd.stopPromptRecordings(context.Background())
}

// additionalContext is out's additionalContext, or "".
func additionalContext(out *hookio.Output) string {
	if out == nil || out.HookSpecificOutput == nil {
		return ""
	}
	return out.HookSpecificOutput.AdditionalContext
}

// TestSessionStartCompact_AnswerDoesNotWaitForTheSessionsIngest is C1.16's regression row, on the
// shipped wiring: the real observer, rehydrator and store (WireObserver), and the SAME session's
// Stop being processed when the compact SessionStart arrives. That Stop holds the observer's
// per-session lock, and the route used to run the whole rehydration inside the observer's
// SessionStart, behind that lock: under load the answer waited for the session's ingest (the rig
// measured p99 918 ms on that wait alone, and live session 2 lost a rehydration to the 10 s reply
// deadline). The rehydration touches none of the observer's per-session state, so the answer — the
// rehydration itself, not the deferred note — must arrive while that lock is still held.
func TestSessionStartCompact_AnswerDoesNotWaitForTheSessionsIngest(t *testing.T) {
	root := t.TempDir()
	var gate *gatedGraph
	_, dd, o := wireTestDaemon(t, root, func(o *Options) {
		g, err := dag.Open(root, o.Cfg, logging.Nop())
		require.NoError(t, err)
		gate = newGatedGraph(g)
		o.Graph = gate
	})
	// The compaction opens the negative-knowledge ledger lazily (WireRehydrator), and the daemon
	// that would close it is never Run here. Windows will not delete an open file.
	t.Cleanup(func() {
		if l := o.LedgerHandle(); l != nil {
			_ = l.Close()
		}
	})
	require.Equal(t, compactAnswerBudget(), dd.compactBudget, "the production bound is in force")
	const sess = core.SessionID("sess-c116-lock")
	ctx := context.Background()

	// Something to rehydrate: the session's first prompt, captured verbatim.
	_, err := dd.svc.ObservePrompt(ctx, hookio.Event{
		HookEventName: "UserPromptSubmit", SessionID: sess, CWD: root,
		Prompt: "Keep the config port at 8443 and tell me the release codeword.",
	})
	require.NoError(t, err)

	// The same session's Stop, held inside the observer's per-session lock.
	gate.arm()
	stopDone := make(chan error, 1)
	go func() {
		stopDone <- dd.svc.ObserveStop(ctx, hookio.Event{HookEventName: "Stop", SessionID: sess, CWD: root}, false)
	}()
	t.Cleanup(gate.open)
	select {
	case <-gate.entered:
	case <-hangGuard(t):
		t.Fatal("the Stop never reached the graph flush it holds the session lock across")
	}

	resp, took := dispatchWithin(t, dd, compactRequest(root, sess), compactTestBound,
		"the compact SessionStart waited for the same session's in-flight Stop")
	require.True(t, resp.OK)
	ac := additionalContext(resp.Output)
	t.Logf("answered in %s with the session's lock held", took)
	require.Contains(t, ac, "# Qompack rehydration", "the answer must be the rehydration itself (took %s)", took)
	require.Contains(t, ac, "8443", "the rehydration carries the captured prompt")
	require.NotContains(t, ac, DeferredNoteTag, "a rehydration that was ready is not deferred")

	// The observer's own SessionStart bookkeeping still runs, once the lock frees, and the Stop it
	// queued behind completes normally.
	gate.open()
	select {
	case err := <-stopDone:
		require.NoError(t, err)
	case <-hangGuard(t):
		t.Fatal("the held Stop never finished")
	}
	joinReplyWork(t, dd)
	require.EqualValues(t, 1, dd.m.Snapshot().Hists["observer.session_start"].N,
		"the observer's SessionStart bookkeeping must still run for a compact start")
	require.Zero(t, dd.m.Snapshot().Counters[counterCompactDeferred])
}

// compactFixture is a daemon whose Rehydrate and SessionStart seams are test doubles, for the rows
// that are about the route's bound rather than about the observer.
type compactFixture struct {
	dd  *daemon
	log *recordingLogger

	mu               sync.Mutex
	sessionStartCtxs []context.Context
	release          chan struct{}
}

func newCompactFixture(t *testing.T, rehydrate func(ctx context.Context, f *compactFixture) (hookio.Output, error)) *compactFixture {
	t.Helper()
	root := t.TempDir()
	f := &compactFixture{log: newRecordingLogger(), release: make(chan struct{})}
	o := NewOptions(root, testConfig())
	o.Log = f.log
	o.Bind(func(s *Services) {
		s.Rehydrate = func(ctx context.Context, _ hookio.Event) (hookio.Output, error) { return rehydrate(ctx, f) }
		s.SessionStart = func(ctx context.Context, _ hookio.Event) (hookio.Output, error) {
			f.mu.Lock()
			f.sessionStartCtxs = append(f.sessionStartCtxs, ctx)
			f.mu.Unlock()
			return hookio.Empty(), nil
		}
	})
	d, err := New(o)
	require.NoError(t, err)
	dd, ok := d.(*daemon)
	require.True(t, ok)
	t.Cleanup(func() { _ = dd.ing.Close() })
	f.dd = dd
	t.Cleanup(func() {
		f.open()
		joinReplyWork(t, dd)
	})
	return f
}

func (f *compactFixture) open() {
	select {
	case <-f.release:
	default:
		close(f.release)
	}
}

// probeLine reports whether ac ends with the §12.1 contract-probe line the route appends.
func hasProbeLine(ac string) bool {
	return strings.Contains(ac, "qompack-contract-probe")
}

// TestSessionStartCompact_LateRehydrationIsAnsweredWithTheDeferredNote pins the bound: a
// rehydration still being built when the answer is due is answered with the explicit deferred note
// — never the empty output the hook client's deadline produced in live session 2 — plus the
// contract probe, counted and Loud. The work is not abandoned: it finishes on its own goroutine.
func TestSessionStartCompact_LateRehydrationIsAnsweredWithTheDeferredNote(t *testing.T) {
	const latePayload = "LATE-PAYLOAD-c116"
	finished := make(chan struct{})
	f := newCompactFixture(t, func(ctx context.Context, f *compactFixture) (hookio.Output, error) {
		defer close(finished)
		<-f.release
		return hookio.SessionStartOutput("<!-- qompack:injected seq=1 ver=1 -->" + latePayload + "<!-- /qompack:injected -->"), nil
	})
	f.dd.compactBudget = 200 * time.Millisecond

	resp, took := dispatchWithin(t, f.dd, compactRequest(f.dd.root, "sess-late"), compactTestBound,
		"a rehydration that never finishes must not hold the answer past its bound")
	require.True(t, resp.OK)
	ac := additionalContext(resp.Output)
	require.True(t, strings.HasPrefix(ac, DeferredNoteTag), "the answer must be the deferred note: %q", ac)
	require.Contains(t, ac, DeferredNotReady)
	require.Contains(t, ac, "expand(tool_use_id=prompt_sess-late_0)")
	require.Contains(t, ac, "dropped()")
	require.True(t, hasProbeLine(ac), "the contract probe is still appended: %q", ac)
	require.NotContains(t, ac, latePayload, "the late rehydration must not appear in this answer")
	require.GreaterOrEqual(t, took, f.dd.compactBudget, "answered before the rehydration was due")

	require.EqualValues(t, 1, f.dd.m.Snapshot().Counters[counterCompactDeferred])
	require.Contains(t, f.log.msgs(logLoud), "daemon: compact SessionStart answered without its rehydration")

	// The rehydration still runs to completion once it can.
	f.open()
	select {
	case <-finished:
	case <-hangGuard(t):
		t.Fatal("the late rehydration never finished")
	}
}

// TestSessionStartCompact_ReadyRehydrationIsTheAnswer is the ordinary case through the same
// machinery: the rehydration the seam returns is the answer, the probe line follows it, and the
// observer's SessionStart runs beside it marked so that it builds no second rehydration.
func TestSessionStartCompact_ReadyRehydrationIsTheAnswer(t *testing.T) {
	const payload = "<!-- qompack:injected seq=3 ver=1 -->\n# Qompack rehydration\n<!-- /qompack:injected -->"
	f := newCompactFixture(t, func(ctx context.Context, _ *compactFixture) (hookio.Output, error) {
		require.False(t, routeRehydrates(ctx), "the route's own rehydration must not carry the bookkeeping mark")
		require.NotNil(t, compactTicketFrom(ctx), "the route's rehydration hands its answer over a ticket")
		return hookio.SessionStartOutput(payload), nil
	})

	resp, _ := dispatchWithin(t, f.dd, compactRequest(f.dd.root, "sess-ready"), compactTestBound, "no answer")
	ac := additionalContext(resp.Output)
	require.True(t, strings.HasPrefix(ac, payload), "the rehydration must lead the answer: %q", ac)
	require.True(t, hasProbeLine(ac))
	require.Zero(t, f.dd.m.Snapshot().Counters[counterCompactDeferred])

	joinReplyWork(t, f.dd)
	f.mu.Lock()
	defer f.mu.Unlock()
	require.Len(t, f.sessionStartCtxs, 1, "the observer's SessionStart bookkeeping runs once")
	require.True(t, routeRehydrates(f.sessionStartCtxs[0]), "and it is marked as bookkeeping only")
}

// TestSessionStartCompact_PanickingRehydrationIsAnsweredAtOnce: a rehydration that panics answers
// the waiting route immediately with the deferred note, rather than leaving it to run out its bound
// and then saying nothing.
func TestSessionStartCompact_PanickingRehydrationIsAnsweredAtOnce(t *testing.T) {
	f := newCompactFixture(t, func(context.Context, *compactFixture) (hookio.Output, error) {
		panic("rehydration boom")
	})
	f.dd.compactBudget = compactTestBound + time.Minute // only the panic can answer in time

	resp, _ := dispatchWithin(t, f.dd, compactRequest(f.dd.root, "sess-panic"), compactTestBound,
		"a panicking rehydration must answer the route at once")
	ac := additionalContext(resp.Output)
	require.True(t, strings.HasPrefix(ac, DeferredNoteTag), "%q", ac)
	require.Contains(t, ac, DeferredFailed)
	require.EqualValues(t, 1, f.dd.m.Snapshot().Counters[counterHandlerPanic])
}

// TestSessionStartCompact_StoppingDaemonAnswersWithTheNote: once Stop has begun joining the reply
// work, no new rehydration can be started, and the answer says why instead of being empty.
func TestSessionStartCompact_StoppingDaemonAnswersWithTheNote(t *testing.T) {
	f := newCompactFixture(t, func(context.Context, *compactFixture) (hookio.Output, error) {
		t.Error("no rehydration may start once Stop has closed the reply-work gate")
		return hookio.Empty(), nil
	})
	joinReplyWork(t, f.dd) // closes the gate, as Stop does

	resp, _ := dispatchWithin(t, f.dd, compactRequest(f.dd.root, "sess-stop"), compactTestBound, "no answer")
	ac := additionalContext(resp.Output)
	require.True(t, strings.HasPrefix(ac, DeferredNoteTag), "%q", ac)
	require.Contains(t, ac, DeferredStopping)
}

// TestSessionStartCompact_PassiveModeStillActsOnNothing: the bound changes nothing about §12.1. A
// degraded-passive daemon injects nothing — no rehydration, no deferred note — and starts no
// rehydration at all.
func TestSessionStartCompact_PassiveModeStillActsOnNothing(t *testing.T) {
	f := newCompactFixture(t, func(context.Context, *compactFixture) (hookio.Output, error) {
		t.Error("no rehydration may start under degraded-passive")
		return hookio.Empty(), nil
	})
	f.dd.monitor.Degrade("forced for test", []contract.Result{
		{ID: "x.forced", OK: false, Severity: contract.SevCritical, Expected: "e", Observed: "o"},
	})

	resp, _ := dispatchWithin(t, f.dd, compactRequest(f.dd.root, "sess-passive"), compactTestBound, "no answer")
	require.True(t, resp.OK)
	require.Empty(t, additionalContext(resp.Output), "degraded-passive injects nothing")
	require.Zero(t, f.dd.m.Snapshot().Counters[counterCompactDeferred])
}

// TestCompactDeferredNote_FitsTheHostCap: the note is a few hundred characters for any session id,
// including a hostile one, so it always reaches the model whole (hookio.HostFieldMaxChars; the
// rehydration's own ceiling is rehydrate.HostContextCeilingChars). An id too long to quote is left
// out of the expand() pointer rather than truncated into a pointer to nothing.
func TestCompactDeferredNote_FitsTheHostCap(t *testing.T) {
	const noteCeiling = 1000 // far under the host's 10,000: the note must never compete for the cap
	for _, sess := range []core.SessionID{
		"", "33e326bf-1cbc-4758-a7e9-5d293c59e05b",
		core.SessionID(strings.Repeat("\U0001F600", deferredNoteMaxSessionRunes)),
		core.SessionID(strings.Repeat("x", 100_000)),
	} {
		for _, reason := range []string{
			DeferredNotReady, DeferredStopping, DeferredFailed, DeferredCheckpointUnreadable, DeferredNoAnswer,
		} {
			note := CompactDeferredNote(sess, reason)
			require.True(t, strings.HasPrefix(note, DeferredNoteTag))
			require.Contains(t, note, reason)
			require.Contains(t, note, "recall(")
			require.Contains(t, note, "dropped()")
			require.LessOrEqual(t, hookio.HostChars(note), noteCeiling, "session %.40q", sess)
			out := hookio.ConformOutput(hookio.EventSessionStart, compactDeferredOutput(sess, reason))
			require.Empty(t, hookio.HostCapOverruns(out))
		}
	}
	require.NotContains(t, CompactDeferredNote(core.SessionID(strings.Repeat("x", 129)), DeferredNotReady), "expand(",
		"an id too long to quote is left out, not truncated")
	require.Contains(t, CompactDeferredNote("abc", DeferredNotReady), "expand(tool_use_id=prompt_abc_0)")
}

// TestCompactAnswerBudget_IsAThirdOfTheManifestTimeout pins the bound's derivation (see
// compactAnswerBudget): a third of the SessionStart hook's manifest timeout, which is half of the
// hook client's own 10 s reply deadline (two thirds of the same timeout).
func TestCompactAnswerBudget_IsAThirdOfTheManifestTimeout(t *testing.T) {
	manifest := time.Duration(manifestHookTimeoutMs(hookEventNameSessionStart)) * time.Millisecond
	require.Positive(t, manifest)
	require.Equal(t, manifest/3, compactAnswerBudget())
	require.Equal(t, defaultCompactAnswerBudget, compactAnswerBudget(),
		"the fallback must agree with the manifest the build ships")
}

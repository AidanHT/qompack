package cli

import (
	"bytes"
	"context"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/daemon"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/obs"
	"github.com/qompack/qompack/internal/paths"
)

// V6 close-out D17b: session-start's whole invocation ends inside the SessionStart manifest timeout.
// Before it, only the reply wait was bounded (10 s); the pre-send step before it — staging the
// binary on Windows, EnsureRunning's poll — was not, and one co-loaded cold start took 21.2 s
// (w3-startroute runs/50), past the host's 15 s.

// TestSessionStartBudget_SplitsTheManifestTimeout pins the owner's numbers: of the 15 s the manifest
// gives SessionStart, hookExitReserve (1.5 s) is kept for the process's start and exit, the reply
// deadline (10 s) and the dial (250 ms) come last, and the pre-send step gets the 3.25 s before
// them. A pre-send step that keeps to its budget leaves the full reply deadline.
func TestSessionStartBudget_SplitsTheManifestTimeout(t *testing.T) {
	spec := sessionStartSpec(nil)
	require.Equal(t, 15*time.Second, spec.hostTimeout, "the manifest's SessionStart timeout")
	require.Equal(t, sessionStartReplyDeadline, spec.deadline)
	require.Equal(t, 1500*time.Millisecond, hookExitReserve)

	began := time.Unix(0, 0)
	b := newHookBudget(began, spec.hostTimeout, spec.deadline, hookConnectDeadlineFloor)
	require.Equal(t, 13500*time.Millisecond, b.doneBy.Sub(began), "the hook's own work ends 1.5 s before the host's timeout")
	require.Equal(t, 3250*time.Millisecond, b.preSendBy.Sub(began), "what is left for the pre-send step")
	require.Equal(t, 13250*time.Millisecond, b.latestPoll.Sub(began), "a late daemon may be waited for until only the dial fits")
	require.Equal(t, sessionStartReplyDeadline, b.replyDeadline(b.preSendBy, spec.deadline, hookConnectDeadlineFloor),
		"a pre-send step inside its budget leaves the full reply deadline")
	require.Equal(t, 2*time.Second, b.replyDeadline(b.preSendBy.Add(8*time.Second), spec.deadline, hookConnectDeadlineFloor),
		"one that ran over leaves only what is left before doneBy")

	require.Zero(t, newHookBudget(began, 0, spec.deadline, hookConnectDeadlineFloor), "no hostTimeout, no bound")
	require.Equal(t, spec.deadline, hookBudget{}.replyDeadline(began, spec.deadline, hookConnectDeadlineFloor))
}

// silentDaemon listens at root's address and takes every request without ever answering it — a
// daemon still starting, or one whose answer is late. It reports how many requests reached it.
func silentDaemon(t *testing.T, root string) *atomic.Int64 {
	t.Helper()
	addr, err := ipc.Resolve(root)
	require.NoError(t, err)
	srv, err := ipc.NewServer(addr, logging.Nop(), obs.New(testClock()), 0)
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	var got atomic.Int64
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = srv.Serve(ctx, func(ctx context.Context, _ ipc.Request) ipc.Response {
			got.Add(1)
			<-ctx.Done()
			return ipc.Response{}
		})
	}()
	t.Cleanup(func() {
		cancel()
		_ = srv.Close()
		<-done
	})
	return &got
}

// budgetTestReply and budgetTestSlack scale the budget rows down from session-start's 10 s so they
// run in seconds: a reply deadline of budgetTestReply, and the margin a loaded machine may add past
// the bound. The bound under test is the same arithmetic session-start uses.
const (
	budgetTestReply = 3 * time.Second
	budgetTestSlack = time.Second
)

// runBudgetedSessionStart runs a session-start with the given spec and source against root and
// returns its stdout, how long it took, and the instant preSend was told to return by.
func runBudgetedSessionStart(t *testing.T, root, source string, spec hookSpec) (string, time.Duration, time.Time) {
	t.Helper()
	var by time.Time
	var mu sync.Mutex
	inner := spec.preSend
	spec.preSend = func(r, s string, st ipc.State, clk core.Clock, b hookBudget) {
		mu.Lock()
		by = b.preSendBy
		mu.Unlock()
		inner(r, s, st, clk, b)
	}
	payload := strings.Replace(string(entryPayload(t, "SessionStart", root)), `"source":"compact"`,
		`"source":"`+source+`"`, 1)
	var out, errw bytes.Buffer
	start := time.Now()
	err := doHook(spec)(context.Background(), Env{
		Getenv:  envWith(map[string]string{"QOMPACK_PROJECT_ROOT": root}),
		Stdin:   strings.NewReader(payload),
		Clock:   testClock(),
		HomeDir: t.TempDir(),
	}, nil, &out, &errw)
	took := time.Since(start)
	require.NoError(t, err, "stderr=%s", errw.String())
	mu.Lock()
	defer mu.Unlock()
	return out.String(), took, by
}

// blockUntil is a pre-send step that runs until at: staging a binary on a loaded machine.
func blockUntil(at func(by time.Time) time.Time) func(string, string, ipc.State, core.Clock, hookBudget) {
	return func(_, _ string, _ ipc.State, _ core.Clock, b hookBudget) {
		<-time.After(time.Until(at(b.preSendBy)))
	}
}

// spooledStart reports whether root's spool holds a session.start line.
func spooledStart(t *testing.T, root string) bool {
	t.Helper()
	files, err := ipc.SpoolFiles(paths.Of(root).Spool)
	require.NoError(t, err)
	return slices.ContainsFunc(files, func(f string) bool {
		b, rerr := paths.ReadFileShared(f)
		require.NoError(t, rerr)
		return strings.Contains(string(b), `"op":"`+string(ipc.OpSessionStart)+`"`)
	})
}

// TestSessionStartBudget_APreSendOverrunCutsTheReplyWait: the pre-send step ran past its budget (a
// staged copy that took seconds to write), so the reply wait is cut to what is left and the hook
// still ends by the bound. It dials, the daemon holds the request, the cut wait expires, and the
// request is spooled like any start whose answer missed its deadline.
func TestSessionStartBudget_APreSendOverrunCutsTheReplyWait(t *testing.T) {
	root := unansweredProject(t, nil)
	requests := silentDaemon(t, root)
	host := hookExitReserve + budgetTestReply + hookConnectDeadlineFloor + hookConnectDeadlineFloor
	bound := host - hookExitReserve
	spec := sessionStartSpec(blockUntil(func(by time.Time) time.Time { return by.Add(2 * time.Second) }))
	spec.hostTimeout, spec.deadline = host, budgetTestReply

	before := time.Now()
	out, took, by := runBudgetedSessionStart(t, root, "startup", spec)
	require.WithinDuration(t, before.Add(bound-budgetTestReply-hookConnectDeadlineFloor), by, budgetTestSlack,
		"preSend is told the instant its budget ends")
	require.Less(t, took, bound+budgetTestSlack,
		"the hook must end by its bound (%s) however long the pre-send step took; it took %s", bound, took)
	require.EqualValues(t, 1, requests.Load(), "with time left the hook still dials")
	require.True(t, spooledStart(t, root), "the unanswered start is spooled")
	require.Equal(t, "{}\n", out)
}

// TestSessionStartBudget_NoTimeLeftSpoolsWithoutDialling: the pre-send step used the whole budget,
// so no answer could be waited for. The hook does not dial a daemon it cannot wait for — the request
// is spooled as a missed start's is — and a compaction gets the deferred note at once.
func TestSessionStartBudget_NoTimeLeftSpoolsWithoutDialling(t *testing.T) {
	root := unansweredProject(t, nil)
	requests := silentDaemon(t, root)
	host := hookExitReserve + budgetTestReply + hookConnectDeadlineFloor + hookConnectDeadlineFloor
	bound := host - hookExitReserve
	spec := sessionStartSpec(blockUntil(func(by time.Time) time.Time {
		return by.Add(budgetTestReply + hookConnectDeadlineFloor + 100*time.Millisecond) // past the bound
	}))
	spec.hostTimeout, spec.deadline = host, budgetTestReply

	out, took, _ := runBudgetedSessionStart(t, root, "compact", spec)
	require.Less(t, took, bound+budgetTestSlack, "the hook ends as soon as the pre-send step does; it took %s", took)
	require.Zero(t, requests.Load(), "no answer can be waited for, so nothing is sent")
	require.True(t, spooledStart(t, root), "the start is spooled for the daemon to replay")
	require.Contains(t, out, daemon.DeferredNoteTag, "a compaction that got no answer says so")
	require.Contains(t, out, daemon.DeferredNoAnswer)
}

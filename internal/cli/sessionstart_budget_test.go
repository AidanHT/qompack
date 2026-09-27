package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/contract"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/daemon"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/obs"
	"github.com/qompack/qompack/internal/paths"
)

// V6 close-out D17b: session-start's whole invocation ends inside the SessionStart manifest timeout.
// Before it, only the reply wait was bounded (10 s); the pre-send step before it — staging the
// binary on Windows, EnsureRunning's poll — was not, and one co-loaded cold start took 21.2 s
// (w3-startroute runs/50), past the host's 15 s.

// TestSessionStartBudget_SplitsTheManifestTimeout pins the owner's numbers (D17b, D21): of the 15 s
// the manifest gives SessionStart, hookExitReserve (1.5 s) is kept for the process's start and
// exit, the reply deadline (10 s) and the dial (250 ms) come last, and a pre-send step that returns
// within the 3.25 s before them leaves the full reply deadline. The find/start step may borrow the
// reply's idle time until 8.25 s, which still leaves the reply D9's 5 s compact bound plus the
// dial; a daemon started late may be waited for until 13.25 s, where only the dial still fits.
func TestSessionStartBudget_SplitsTheManifestTimeout(t *testing.T) {
	spec := sessionStartSpec(nil)
	require.Equal(t, 15*time.Second, spec.hostTimeout, "the manifest's SessionStart timeout")
	require.Equal(t, sessionStartReplyDeadline, spec.deadline)
	require.Equal(t, 1500*time.Millisecond, hookExitReserve)
	require.Equal(t, 5*time.Second, spec.minReply, "the borrowing leaves the reply D9's compact bound")
	require.Equal(t, daemon.CompactAnswerBudget(), spec.minReply, "the daemon's own bound, not a copy")

	began := time.Unix(0, 0)
	b := newHookBudget(began, spec.hostTimeout, spec.deadline, hookConnectDeadlineFloor, spec.minReply)
	require.Equal(t, 13500*time.Millisecond, b.doneBy.Sub(began), "the hook's own work ends 1.5 s before the host's timeout")
	require.Equal(t, 3250*time.Millisecond, b.preSendBy.Sub(began), "what is left before a full reply deadline")
	require.Equal(t, 8250*time.Millisecond, b.borrowBy.Sub(began), "the find/start step may borrow until 8.25 s")
	require.Equal(t, 13250*time.Millisecond, b.latestPoll.Sub(began), "a late daemon may be waited for until only the dial fits")
	require.Equal(t, sessionStartReplyDeadline, b.replyDeadline(b.preSendBy, spec.deadline, hookConnectDeadlineFloor),
		"a pre-send step inside 3.25 s leaves the full reply deadline")
	require.Equal(t, 7250*time.Millisecond, b.replyDeadline(began.Add(6*time.Second), spec.deadline, hookConnectDeadlineFloor),
		"a daemon found at 6 s leaves the reply what is left before doneBy")
	require.Equal(t, 5250*time.Millisecond, b.replyDeadline(began.Add(8*time.Second), spec.deadline, hookConnectDeadlineFloor),
		"a daemon found at 8 s still leaves more than the compact bound")
	require.Equal(t, spec.minReply, b.replyDeadline(b.borrowBy, spec.deadline, hookConnectDeadlineFloor),
		"at the borrow limit exactly the compact bound is left")
	require.Equal(t, 2*time.Second, b.replyDeadline(b.preSendBy.Add(8*time.Second), spec.deadline, hookConnectDeadlineFloor),
		"a step that ran over leaves only what is left before doneBy")

	require.Equal(t, b.preSendBy, newHookBudget(began, spec.hostTimeout, spec.deadline, hookConnectDeadlineFloor, 0).borrowBy,
		"no minReply lends nothing")
	require.Equal(t, b.preSendBy, newHookBudget(began, spec.hostTimeout, spec.deadline, hookConnectDeadlineFloor, spec.deadline).borrowBy,
		"a minReply as long as the reply lends nothing")
	require.Zero(t, newHookBudget(began, 0, spec.deadline, hookConnectDeadlineFloor, spec.minReply), "no hostTimeout, no bound")
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

// V6 close-out D21: session-start's find/start step may borrow the reply wait's idle time. Its poll
// for a daemon on its way runs until borrowBy, 8.25 s from doHook's first statement — the last
// instant that still leaves the reply D9's 5 s compact bound (daemon.CompactAnswerBudget) plus the
// dial — instead of stopping at 3.25 s, where the full 10 s reply would still fit. The reply wait
// is then what is left, min(10 s, doneBy - now - dial). The rows below run session-start through
// its production preSend (ensureDaemonRunning, so daemon.EnsureRunningUntil) with the manifest's
// real 15 s budget, against a project whose spawn.lock holds a fresh claim: a daemon is on its way,
// so session-start waits for it and spawns nothing. The rows run in parallel; each waits for real.

// lateDaemonAnswer is what a lateDaemon answers a session.start with, so a row can tell the
// daemon's answer from the empty answer and the deferred note an unanswered start gets.
const lateDaemonAnswer = "late-daemon-answer"

// lateDaemon is a daemon on its way: it starts listening at a given instant, removes the spawn
// claim that announced it the way Run does (listen, serve, then delete run/spawn.lock), and answers
// every request after holding it for a given time.
type lateDaemon struct {
	mu       sync.Mutex
	srv      ipc.Server
	received int
	err      error
}

// startLateDaemon brings a lateDaemon up for root at the instant at, holding each request for hold.
func startLateDaemon(t *testing.T, root string, at time.Time, hold time.Duration) *lateDaemon {
	t.Helper()
	addr, err := ipc.Resolve(root)
	require.NoError(t, err)
	d := &lateDaemon{}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		select {
		case <-time.After(time.Until(at)):
		case <-ctx.Done():
			return
		}
		srv, lerr := ipc.NewServer(addr, logging.Nop(), obs.New(testClock()), 0)
		d.mu.Lock()
		d.srv, d.err = srv, lerr
		d.mu.Unlock()
		if lerr != nil {
			return
		}
		served := make(chan struct{})
		go func() {
			defer close(served)
			_ = srv.Serve(ctx, func(ctx context.Context, _ ipc.Request) ipc.Response {
				d.mu.Lock()
				d.received++
				d.mu.Unlock()
				select {
				case <-time.After(hold):
				case <-ctx.Done():
					return ipc.Response{}
				}
				out := hookio.SessionStartOutput(lateDaemonAnswer)
				return ipc.Response{OK: true, Mode: contract.ModeFull, Output: &out}
			})
		}()
		removeSpawnClaim(root)
		<-served
	}()
	t.Cleanup(func() {
		cancel()
		d.mu.Lock()
		if d.srv != nil {
			_ = d.srv.Close()
		}
		d.mu.Unlock()
		<-done
	})
	return d
}

// requests reports how many requests reached d, and whether it failed to listen.
func (d *lateDaemon) requests() (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.received, d.err
}

// removeSpawnClaim deletes root's run/spawn.lock, as a daemon does once it listens. The lock is
// created read-only (paths.CreateNew), which blocks the delete on Windows, so the mode goes first.
func removeSpawnClaim(root string) {
	p := filepath.Join(paths.Of(root).Run, "spawn.lock")
	_ = os.Chmod(paths.Long(p), 0o600)
	_ = os.Remove(paths.Long(p))
}

// claimedProject is an unanswered project whose spawn.lock holds a fresh claim, stamped by the
// hook's own clock: another spawner's daemon is on its way.
func claimedProject(t *testing.T) string {
	t.Helper()
	root := unansweredProject(t, nil)
	_, claim := ipc.ClaimSpawn(root, testClock())
	require.Equal(t, ipc.SpawnClaimed, claim)
	return root
}

// runLiveSessionStart runs session-start through its production preSend with the manifest's own
// budget. up, when set, is called with the instant of doHook's first statement (the budget's
// origin) just before the poll begins, so a row can bring a daemon up at an offset from it. It
// returns the hook's stdout, how long the invocation took, and the budget the hook ran under.
func runLiveSessionStart(t *testing.T, root, source string, up func(began time.Time)) (string, time.Duration, hookBudget) {
	t.Helper()
	spec := sessionStartSpec(nil)
	var got hookBudget
	spec.preSend = func(r, self string, st ipc.State, clk core.Clock, b hookBudget) {
		got = b
		if up != nil {
			up(b.doneBy.Add(-(spec.hostTimeout - hookExitReserve)))
		}
		ensureDaemonRunning(r, self, st, clk, b)
	}
	payload := strings.Replace(string(entryPayload(t, "SessionStart", root)), `"source":"compact"`,
		`"source":"`+source+`"`, 1)
	var out, errw bytes.Buffer
	start := time.Now()
	err := doHook(spec)(context.Background(), Env{
		Getenv: envWith(map[string]string{"QOMPACK_PROJECT_ROOT": root}),
		Stdin:  strings.NewReader(payload),
		Clock:  testClock(),
		// A self that names no file: ensureDaemonRunning runs only with a self, and the fresh claim
		// means nothing is spawned from it.
		Self:    filepath.Join(t.TempDir(), "never-spawned"),
		HomeDir: t.TempDir(),
	}, nil, &out, &errw)
	took := time.Since(start)
	require.NoError(t, err, "stderr=%s", errw.String())
	return out.String(), took, got
}

// TestSessionStartBudget_ADaemonUpWhileTheStepMayBorrowIsAnswered: a daemon on its way that comes
// up at 4 s, 6 s or 8 s — after the 3.25 s where the full reply would still fit, before the 8.25 s
// the step may borrow to — is found by the poll and answers; nothing is spooled.
func TestSessionStartBudget_ADaemonUpWhileTheStepMayBorrowIsAnswered(t *testing.T) {
	for _, upAfter := range []time.Duration{4 * time.Second, 6 * time.Second, 8 * time.Second} {
		t.Run(upAfter.String(), func(t *testing.T) {
			t.Parallel()
			root := claimedProject(t)
			var d *lateDaemon
			out, took, _ := runLiveSessionStart(t, root, "startup", func(began time.Time) {
				d = startLateDaemon(t, root, began.Add(upAfter), 0)
			})
			n, lerr := d.requests()
			require.NoError(t, lerr, "the late daemon could not listen")
			require.Contains(t, out, lateDaemonAnswer, "a daemon up at %s is answered, not given up on", upAfter)
			require.Equal(t, 1, n, "the start reached the daemon")
			require.False(t, spooledStart(t, root), "an answered start is not spooled")
			require.Less(t, took, upAfter+budgetTestSlack, "the hook ends once the daemon answers; it took %s", took)
		})
	}
}

// TestSessionStartBudget_ADaemonThatNeverComesUpIsSpooledAtTheBorrowLimit: the daemon on its way
// never listens. The poll gives up at the borrow limit, 8.25 s, the hook's dial fails at once and
// the start is spooled, and the whole hook ends inside the 15 s manifest timeout.
func TestSessionStartBudget_ADaemonThatNeverComesUpIsSpooledAtTheBorrowLimit(t *testing.T) {
	t.Parallel()
	const borrowFor = 8250 * time.Millisecond // doneBy (13.5 s) - dial (250 ms) - compact bound (5 s)
	root := claimedProject(t)
	out, took, _ := runLiveSessionStart(t, root, "startup", nil)
	require.GreaterOrEqual(t, took, borrowFor, "the poll runs to the borrow limit; the hook took %s", took)
	require.Less(t, took, borrowFor+budgetTestSlack, "and the hook spools once it is reached; it took %s", took)
	require.Less(t, took, sessionStartHostTimeout()-hookExitReserve, "the whole hook ends inside the manifest timeout")
	require.True(t, spooledStart(t, root), "the unanswered start is spooled for the daemon to replay")
	require.Equal(t, "{}\n", out)
}

// TestSessionStartBudget_ACompactStartUpAt8sKeepsTheCompactBound: a compaction whose daemon comes
// up at 8 s still leaves the reply more than D9's 5 s compact bound, so a daemon that takes its
// whole bound to answer — the deferred note it sends when the rehydration is late — is heard,
// rather than the start being spooled with the client's own note.
func TestSessionStartBudget_ACompactStartUpAt8sKeepsTheCompactBound(t *testing.T) {
	t.Parallel()
	const upAfter = 8 * time.Second
	root := claimedProject(t)
	var d *lateDaemon
	out, took, _ := runLiveSessionStart(t, root, compactSource, func(began time.Time) {
		d = startLateDaemon(t, root, began.Add(upAfter), daemon.CompactAnswerBudget())
	})
	n, lerr := d.requests()
	require.NoError(t, lerr, "the late daemon could not listen")
	require.Contains(t, out, lateDaemonAnswer, "the daemon's answer at its compact bound reaches the host")
	require.NotContains(t, out, daemon.DeferredNoAnswer, "not the client's own no-answer note")
	require.Equal(t, 1, n)
	require.False(t, spooledStart(t, root), "an answered compaction is not spooled")
	require.Less(t, took, sessionStartHostTimeout()-hookExitReserve+budgetTestSlack,
		"the hook still ends by its bound; it took %s", took)
}

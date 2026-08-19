package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/contract"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/sketch"
)

// testConfig returns config.Defaults(), the sane baseline every daemon-level test builds on.
func testConfig() config.Config { return config.Defaults() }

// uniqueTestAddr returns a QOMPACK_IPC_ADDR value naming an endpoint private to this test: a
// per-test-uniquely-named pipe on Windows, a socket inside t.TempDir() on POSIX. Only tests that
// actually call Daemon.Run need this — dispatchOp-driven tests never touch the transport.
func uniqueTestAddr(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		name := `\\.\pipe\qompack-test-` + strconv.FormatInt(time.Now().UnixNano(), 36)
		return "pipe:" + name
	}
	return "unix:" + filepath.Join(t.TempDir(), "d.sock")
}

// TestNew_SucceedsWithNoServices is the property waves 1-2 depend on: a daemon constructs from an
// Options whose service members are all nil.
func TestNew_SucceedsWithNoServices(t *testing.T) {
	t.Parallel()

	d, err := New(Options{ProjectRoot: t.TempDir()})
	require.NoError(t, err)
	require.NotNil(t, d)
	require.NotNil(t, d.Registry())
	require.NotNil(t, d.Idle())
}

// TestSketchSet_CarriesAllFourStructures pins the SP-01 spelling of SketchSet and its
// assignability into Options.
func TestSketchSet_CarriesAllFourStructures(t *testing.T) {
	t.Parallel()

	s := &SketchSet{
		Tried:   sketch.NewBloom(512, 0.01),
		Touch:   sketch.NewCMS(0.001, 0.01),
		Explore: sketch.NewHLL(14),
		Top:     sketch.NewMisraGries(64),
	}
	require.NotNil(t, s.Tried)
	require.NotNil(t, s.Touch)
	require.NotNil(t, s.Explore)
	require.NotNil(t, s.Top)

	d, err := New(Options{ProjectRoot: t.TempDir(), Sketches: s})
	require.NoError(t, err)
	require.NotNil(t, d)
}

// allOps is the 13-route table TestServicesAllNil and friends drive.
var allOps = []ipc.Op{
	ipc.OpObserveTool, ipc.OpObservePrompt, ipc.OpObserveStop, ipc.OpSessionStart,
	ipc.OpCheckpoint, ipc.OpFlush, ipc.OpStatus, ipc.OpMCP,
	ipc.OpAdminPing, ipc.OpAdminDrain, ipc.OpAdminReload, ipc.OpAdminIdle, ipc.OpAdminShutdown,
}

// TestServicesAllNil drives every one of the 13 ops through the daemon with a fully nil Services
// set: every response must be OK:true except mcp, which reports OK:false "mcp not built" — and no
// call may panic.
func TestServicesAllNil(t *testing.T) {
	t.Parallel()
	require.Len(t, allOps, 13)

	root := t.TempDir()
	d, err := New(Options{ProjectRoot: root, Cfg: testConfig(), Log: logging.Nop()})
	require.NoError(t, err)
	dd, ok := d.(*daemon)
	require.True(t, ok)
	// dd.Stop is idempotent (sync.Once) and synchronous, so calling it here — rather than the
	// bare dd.ing.Close() an earlier revision used — deterministically waits out admin.shutdown's
	// own asynchronous Stop() goroutine (dispatched below) instead of racing it (fix round 1,
	// M-10): sync.Once.Do blocks every caller, including this one, until whichever call actually
	// runs the body has finished.
	t.Cleanup(func() { _ = dd.Stop(context.Background()) })

	for _, op := range allOps {
		sess := core.SessionID("sess-" + string(op))
		ev := &hookio.Event{HookEventName: "x", SessionID: sess, CWD: root, TranscriptPath: filepath.Join(root, "t.jsonl")}
		req := ipc.Request{Op: op, Session: sess, Reply: true, Event: ev, TS: core.NowMilli(dd.clk)}

		var resp ipc.Response
		require.NotPanics(t, func() { resp = dd.dispatchOp(context.Background(), req) }, "op %s", op)

		if op == ipc.OpMCP {
			require.False(t, resp.OK, "op %s", op)
			require.Equal(t, "mcp not built", resp.Err)
			continue
		}
		require.True(t, resp.OK, "op %s must ACK even with nil Services: %+v", op, resp)
	}

	// The WAL must hold every hot-path event even though no Services function ever ran (fix
	// round 1, M-9: observe.prompt was missing from this check).
	for _, op := range []ipc.Op{ipc.OpObserveTool, ipc.OpObserveStop, ipc.OpObservePrompt} {
		sess := "sess-" + string(op)
		walPath := filepath.Join(paths.Of(root).Spool, "wal-"+sess+".ndjson")
		_, err := os.Stat(walPath)
		require.NoError(t, err, "op %s must be WAL'd", op)
	}
}

// TestObservePromptRepliesWithinDeadline: a bound ObservePrompt seam's Output reaches the caller.
func TestObservePromptRepliesWithinDeadline(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	o := Options{ProjectRoot: root, Cfg: testConfig(), Log: logging.Nop()}
	o.Bind(func(s *Services) {
		s.ObservePrompt = func(context.Context, hookio.Event) (hookio.Output, error) {
			return hookio.Output{HookSpecificOutput: &hookio.HSO{HookEventName: "UserPromptSubmit", AdditionalContext: "x"}}, nil
		}
	})
	d, err := New(o)
	require.NoError(t, err)
	dd, ok := d.(*daemon)
	require.True(t, ok)
	t.Cleanup(func() { _ = dd.ing.Close() })

	ev := &hookio.Event{HookEventName: "UserPromptSubmit", SessionID: "sess-1", CWD: root}
	resp := dd.dispatchOp(context.Background(), ipc.Request{Op: ipc.OpObservePrompt, Session: "sess-1", Reply: true, Event: ev})
	require.True(t, resp.OK)
	require.NotNil(t, resp.Output)
	require.NotNil(t, resp.Output.HookSpecificOutput)
	require.Equal(t, "x", resp.Output.HookSpecificOutput.AdditionalContext)
}

// TestObservePromptBlockingVariantTimesOut: an ObservePrompt seam that ignores its context and
// blocks past promptReplyDeadline still returns hookio.Empty() promptly — a prompt is never
// blocked on the daemon.
func TestObservePromptBlockingVariantTimesOut(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	o := Options{ProjectRoot: root, Cfg: testConfig(), Log: logging.Nop()}
	o.Bind(func(s *Services) {
		s.ObservePrompt = func(context.Context, hookio.Event) (hookio.Output, error) {
			block := make(chan struct{}) // never signaled: blocks forever, deliberately ignoring ctx
			<-block
			return hookio.Output{HookSpecificOutput: &hookio.HSO{AdditionalContext: "late"}}, nil
		}
	})
	d, err := New(o)
	require.NoError(t, err)
	dd, ok := d.(*daemon)
	require.True(t, ok)
	t.Cleanup(func() { _ = dd.ing.Close() })

	ev := &hookio.Event{HookEventName: "UserPromptSubmit", SessionID: "sess-2", CWD: root}
	start := time.Now()
	resp := dd.dispatchOp(context.Background(), ipc.Request{Op: ipc.OpObservePrompt, Session: "sess-2", Reply: true, Event: ev})
	elapsed := time.Since(start)

	require.True(t, resp.OK)
	require.NotNil(t, resp.Output)
	require.Nil(t, resp.Output.HookSpecificOutput, "a timed-out ObservePrompt must fall back to hookio.Empty()")
	require.Less(t, elapsed, time.Second, "the route must not block past promptReplyDeadline")
}

// TestObservePromptWALFailureIsObservable is the fix round 2 FR-2 regression: unlike
// observe.tool/stop, whose failure paths return OK:false (NAK, which the client answers by
// spooling — the event stays durable either way), observe.prompt always ACKs, so a WAL-append
// failure was the one hot-path event that could previously vanish with zero observability. It
// must now log a Warn and increment counterL0AcceptError.
func TestObservePromptWALFailureIsObservable(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	// Force ingest.Accept's own os.MkdirAll(spoolDir) to fail: a regular file sits where
	// .qompack must be a directory, so no path beneath it — spool/ included — can ever be
	// created. This is deterministic and cross-platform, unlike simulating a real disk-full or
	// permission error.
	require.NoError(t, os.WriteFile(filepath.Join(root, ".qompack"), []byte("not a directory"), 0o600))

	d, err := New(Options{ProjectRoot: root, Cfg: testConfig(), Log: logging.Nop()})
	require.NoError(t, err)
	dd, ok := d.(*daemon)
	require.True(t, ok)
	t.Cleanup(func() { _ = dd.ing.Close() })

	ev := &hookio.Event{HookEventName: "UserPromptSubmit", SessionID: "sess-1", CWD: root}
	resp := dd.dispatchOp(context.Background(), ipc.Request{Op: ipc.OpObservePrompt, Session: "sess-1", Reply: true, Event: ev})

	// The reply flow is unaffected: observe.prompt still ACKs even though its WAL append failed
	// — a prompt is never blocked or refused because of it.
	require.True(t, resp.OK)
	require.Equal(t, int64(1), dd.m.Counter(counterL0AcceptError).Value(),
		"a failed WAL append must be observable via the counter")
}

// TestDegradedPassiveSuppressesActingPaths: forced ModeDegradedPassive suppresses every acting
// path (SessionStart/ObservePrompt/PreCompact output and act.-prefixed idle tasks) while leaving
// every response OK.
func TestDegradedPassiveSuppressesActingPaths(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	var sessionStartCalled, preCompactCalled bool
	o := Options{ProjectRoot: root, Cfg: testConfig(), Log: logging.Nop()}
	o.Bind(func(s *Services) {
		s.SessionStart = func(context.Context, hookio.Event) (hookio.Output, error) {
			sessionStartCalled = true
			return hookio.Output{HookSpecificOutput: &hookio.HSO{HookEventName: "SessionStart", AdditionalContext: "ctx"}}, nil
		}
		s.PreCompact = func(context.Context, hookio.Event) (hookio.Output, error) {
			preCompactCalled = true
			return hookio.Output{HookSpecificOutput: &hookio.HSO{HookEventName: "PreCompact", CustomInstructions: "instr"}}, nil
		}
		s.ObservePrompt = func(context.Context, hookio.Event) (hookio.Output, error) {
			return hookio.Output{HookSpecificOutput: &hookio.HSO{AdditionalContext: "prompt"}}, nil
		}
	})
	d, err := New(o)
	require.NoError(t, err)
	dd, ok := d.(*daemon)
	require.True(t, ok)
	t.Cleanup(func() { _ = dd.ing.Close() })
	dd.monitor.Degrade("forced for test", []contract.Result{
		{ID: "x.forced", OK: false, Severity: contract.SevCritical, Expected: "e", Observed: "o"},
	})

	ev := &hookio.Event{HookEventName: "SessionStart", SessionID: "sess-1", CWD: root, Source: "startup"}
	resp := dd.dispatchOp(context.Background(), ipc.Request{Op: ipc.OpSessionStart, Session: "sess-1", Reply: true, Event: ev})
	require.True(t, resp.OK)
	require.NotNil(t, resp.Output)
	require.Nil(t, resp.Output.HookSpecificOutput, "no additionalContext under degraded-passive")
	require.False(t, sessionStartCalled)

	ev2 := &hookio.Event{HookEventName: "UserPromptSubmit", SessionID: "sess-1", CWD: root}
	resp2 := dd.dispatchOp(context.Background(), ipc.Request{Op: ipc.OpObservePrompt, Session: "sess-1", Reply: true, Event: ev2})
	require.True(t, resp2.OK)
	require.NotNil(t, resp2.Output)
	require.Nil(t, resp2.Output.HookSpecificOutput)

	ev3 := &hookio.Event{HookEventName: "PreCompact", SessionID: "sess-1", CWD: root}
	resp3 := dd.dispatchOp(context.Background(), ipc.Request{Op: ipc.OpCheckpoint, Session: "sess-1", Reply: true, Event: ev3})
	require.True(t, resp3.OK)
	require.NotNil(t, resp3.Output)
	require.Nil(t, resp3.Output.HookSpecificOutput, "no customInstructions under degraded-passive")
	require.False(t, preCompactCalled)

	dd.idle.Register("act.checkpoint", 10, func(context.Context) error { return nil })
	ran, err := dd.idle.RunOnce(context.Background(), time.Second)
	require.NoError(t, err)
	require.NotContains(t, ran, "act.checkpoint")
}

// TestDegradedPassiveStillRecords: L0/L1 recording keeps running under degraded-passive — 20
// observe.tool events all WAL and all reach the bound ObserveTool seam — and SP-05's own idle
// tasks (none act.-prefixed) all still run.
func TestDegradedPassiveStillRecords(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	var mu sync.Mutex
	calls := 0
	o := Options{ProjectRoot: root, Cfg: testConfig(), Log: logging.Nop()}
	o.Bind(func(s *Services) {
		s.ObserveTool = func(context.Context, hookio.Event) error {
			mu.Lock()
			calls++
			mu.Unlock()
			return nil
		}
	})
	d, err := New(o)
	require.NoError(t, err)
	dd, ok := d.(*daemon)
	require.True(t, ok)
	t.Cleanup(func() { _ = dd.ing.Close() })
	dd.monitor.Degrade("forced for test", []contract.Result{
		{ID: "x.forced", OK: false, Severity: contract.SevCritical},
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	dd.ing.Start(ctx, 2, dd.runIngested)

	for i := 0; i < 20; i++ {
		sess := core.SessionID(fmt.Sprintf("sess-%d", i))
		ev := &hookio.Event{HookEventName: "PostToolUse", SessionID: sess, CWD: root}
		resp := dd.dispatchOp(context.Background(), ipc.Request{Op: ipc.OpObserveTool, Session: sess, Event: ev, TS: core.NowMilli(dd.clk)})
		require.True(t, resp.OK)
	}

	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return calls == 20
	}, 2*time.Second, 10*time.Millisecond, "all 20 events must reach ObserveTool")

	ran, err := dd.idle.RunOnce(context.Background(), time.Second)
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"drain", "sketches", "metrics"}, ran)
}

// TestModeOffSkipsIngest: once the monitor reports ModeOff, observe.tool ACKs without ever
// writing the WAL or calling the bound seam.
func TestModeOffSkipsIngest(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	called := false
	cfg := testConfig()
	cfg.Runtime.Mode = "off"
	o := Options{ProjectRoot: root, Cfg: cfg, Log: logging.Nop()}
	o.Bind(func(s *Services) {
		s.ObserveTool = func(context.Context, hookio.Event) error { called = true; return nil }
	})
	d, err := New(o)
	require.NoError(t, err)
	dd, ok := d.(*daemon)
	require.True(t, ok)
	t.Cleanup(func() { _ = dd.ing.Close() })

	ev := &hookio.Event{HookEventName: "SessionStart", SessionID: "sess-1", CWD: root, Source: "startup"}
	resp := dd.dispatchOp(context.Background(), ipc.Request{Op: ipc.OpSessionStart, Session: "sess-1", Reply: true, Event: ev})
	require.True(t, resp.OK)
	require.Equal(t, contract.ModeOff, dd.monitor.Mode())

	ev2 := &hookio.Event{HookEventName: "PostToolUse", SessionID: "sess-1", CWD: root}
	resp2 := dd.dispatchOp(context.Background(), ipc.Request{Op: ipc.OpObserveTool, Session: "sess-1", Event: ev2, TS: core.NowMilli(dd.clk)})
	require.True(t, resp2.OK)
	require.False(t, called)

	walPath := filepath.Join(paths.Of(root).Spool, "wal-sess-1.ndjson")
	_, statErr := os.Stat(walPath)
	require.True(t, os.IsNotExist(statErr), "the WAL file must never be created under ModeOff")
}

// TestNAKDuplicateIsDedupedOnDrain: a request WAL'd and NAK'd under HotSpool, then duplicated by
// the client's own spool, is dispatched exactly once when drained.
func TestNAKDuplicateIsDedupedOnDrain(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	var mu sync.Mutex
	calls := 0
	o := Options{ProjectRoot: root, Cfg: testConfig(), Log: logging.Nop()}
	o.Bind(func(s *Services) {
		s.ObserveTool = func(context.Context, hookio.Event) error {
			mu.Lock()
			calls++
			mu.Unlock()
			return nil
		}
	})
	d, err := New(o)
	require.NoError(t, err)
	dd, ok := d.(*daemon)
	require.True(t, ok)
	t.Cleanup(func() { _ = dd.ing.Close() })
	dd.drain.Store(newDrainer(DrainConfig{
		Root: root, Log: logging.Nop(), Metrics: dd.m, Clock: dd.clk,
		Dispatch: dd.drainDispatch, Seen: dd.ing.seen, IsLive: dd.sessionIsLive,
	}))
	dd.registry.SetHotMode(ipc.HotSpool, "test")

	ev := &hookio.Event{HookEventName: "PostToolUse", SessionID: "sess-1", CWD: root}
	req := ipc.Request{Op: ipc.OpObserveTool, Session: "sess-1", Event: ev, TS: core.NowMilli(dd.clk)}

	resp := dd.dispatchOp(context.Background(), req)
	require.False(t, resp.OK, "a fire-and-forget hot-path request must be NAK'd once spooling")

	line, err := ipc.EncodeRequest(req)
	require.NoError(t, err)
	spoolPath := filepath.Join(paths.Of(root).Spool, "client-99999.ndjson")
	require.NoError(t, os.MkdirAll(filepath.Dir(spoolPath), 0o700))
	require.NoError(t, os.WriteFile(paths.Long(spoolPath), append(line, '\n'), 0o600))

	n, err := dd.Drain(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, n, "exactly one of the two identical lines is actually dispatched")
	require.Equal(t, 1, calls, "the handler must see the request exactly once")

	_, statErr := os.Stat(paths.Long(spoolPath))
	require.True(t, os.IsNotExist(statErr), "the client spool file must be consumed")
}

// drainDeadlockGuard bounds how long a Drain call under test is allowed to take before it is
// presumed permanently wedged — generous relative to the sub-second work these tests actually do,
// tight enough that a genuine self-deadlock (fix round 1, Critical C-1) fails the test instead of
// hanging the whole suite (and, under `go test`, eventually the test binary's own timeout).
const drainDeadlockGuard = 10 * time.Second

// TestDrainOfSpooledFlushLineDoesNotDeadlock is the Critical C-1 regression: a flush line
// (Op:"flush", Reply:true — exactly what a SessionEnd hook firing while the daemon is down
// leaves behind, since ipc.client.Send spools every op on every connect failure, not just the
// hot-path ones) sitting in a client spool file must drain without the drain goroutine calling
// back into Daemon.Drain on itself. Before the round-1 fix, this test hung forever on
// drainer.Drain's own non-reentrant mutex — exactly the trap the startup drain (Run, before Serve
// ever accepts a connection) and Stop's own bounded drain both sit in.
func TestDrainOfSpooledFlushLineDoesNotDeadlock(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	d, err := New(Options{ProjectRoot: root, Cfg: testConfig(), Log: logging.Nop()})
	require.NoError(t, err)
	dd, ok := d.(*daemon)
	require.True(t, ok)
	t.Cleanup(func() { _ = dd.ing.Close() })
	dd.drain.Store(newDrainer(DrainConfig{
		Root: root, Log: logging.Nop(), Metrics: dd.m, Clock: dd.clk,
		Dispatch: dd.drainDispatch, Seen: dd.ing.seen, IsLive: dd.sessionIsLive,
	}))

	flushReq := ipc.Request{
		Op: ipc.OpFlush, Session: "sess-1", Reply: true,
		Event: &hookio.Event{HookEventName: "SessionEnd", SessionID: "sess-1", CWD: root},
	}
	line, err := ipc.EncodeRequest(flushReq)
	require.NoError(t, err)
	spoolPath := filepath.Join(paths.Of(root).Spool, "client-88888.ndjson")
	require.NoError(t, os.MkdirAll(filepath.Dir(spoolPath), 0o700))
	require.NoError(t, os.WriteFile(paths.Long(spoolPath), append(line, '\n'), 0o600))

	type result struct {
		n   int
		err error
	}
	done := make(chan result, 1)
	go func() {
		n, derr := dd.Drain(context.Background())
		done <- result{n, derr}
	}()

	select {
	case r := <-done:
		require.NoError(t, r.err)
		require.Equal(t, 1, r.n)
	case <-time.After(drainDeadlockGuard):
		t.Fatal("Drain of a spooled flush line did not return — self-deadlock on drainer.Drain's own mutex")
	}

	_, statErr := os.Stat(paths.Long(spoolPath))
	require.True(t, os.IsNotExist(statErr), "the spooled flush line must be consumed")
}

// TestStartupDrainOfSpooledFlushLineDoesNotWedgeRun is Critical C-1's end-to-end shape: a flush
// line spooled while the daemon was down must not wedge Run's OWN startup drain (which runs
// before Serve ever accepts a connection) — a daemon.lock held forever by a process that never
// listens is worse than the deadlock alone, since every client then fails to connect too.
func TestStartupDrainOfSpooledFlushLineDoesNotWedgeRun(t *testing.T) {
	root := t.TempDir()
	t.Setenv("QOMPACK_IPC_ADDR", uniqueTestAddr(t))

	flushReq := ipc.Request{
		Op: ipc.OpFlush, Session: "sess-1", Reply: true,
		Event: &hookio.Event{HookEventName: "SessionEnd", SessionID: "sess-1", CWD: root},
	}
	line, err := ipc.EncodeRequest(flushReq)
	require.NoError(t, err)
	spoolPath := filepath.Join(paths.Of(root).Spool, "client-77777.ndjson")
	require.NoError(t, os.MkdirAll(filepath.Dir(spoolPath), 0o700))
	require.NoError(t, os.WriteFile(paths.Long(spoolPath), append(line, '\n'), 0o600))

	d, err := New(Options{ProjectRoot: root, Cfg: testConfig(), Log: logging.Nop(), Clock: core.SystemClock()})
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), drainDeadlockGuard)
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- d.Run(ctx) }()

	// Run must reach Serve and accept a connection — the very thing a wedged startup drain
	// prevents, since Run never reaches `go server.Serve(...)` until Drain has returned.
	require.Eventually(t, func() bool {
		addr, rerr := ipc.Resolve(root)
		if rerr != nil {
			return false
		}
		return ipc.Probe(addr, 100*time.Millisecond)
	}, drainDeadlockGuard, 50*time.Millisecond, "Run never started accepting connections — startup drain is likely wedged")

	cancel()
	select {
	case <-errCh:
	case <-time.After(drainDeadlockGuard):
		t.Fatal("Run did not shut down after cancellation")
	}
}

// redrainTestBound is how long TestRedrainOnFirstServedRequest gives the re-drain, and it is the
// whole point of the test: it is SIX TIMES SMALLER than idleTickMax (30s), which is the only other
// mechanism in the daemon that could drain a spool file written after the startup drain has
// already scanned the directory. A pass inside this bound therefore cannot be the idle tick in
// disguise (V2-MERGE-25 ①), and a failure at this bound means the re-drain did not fire, not that
// the host was slow.
const (
	redrainTestBound = idleTickMax / 6
	redrainTestTick  = 25 * time.Millisecond
	// redrainDialBound is the connect/ACK budget for this test's own admin client. It is wide
	// relative to config.Defaults()'s ConnectDeadlineMs (5ms, tuned for an already-warm daemon)
	// because the very first dial into a daemon that has just started is exactly the case that
	// budget is NOT sized for — the same reason internal/cli carries hookConnectDeadlineFloor.
	redrainDialBound = 500 * time.Millisecond
)

// writeClientSpoolLine writes req as a one-line client-*.ndjson spool file under root's spool
// directory, the way a client that could not reach the daemon would have left it, and returns the
// path. The drainer deletes a client-* file once it has fully consumed it, so the path's absence
// is the observable "this entry was drained".
func writeClientSpoolLine(t *testing.T, root, name string, req ipc.Request) string {
	t.Helper()
	line, err := ipc.EncodeRequest(req)
	require.NoError(t, err)
	p := filepath.Join(paths.Of(root).Spool, name)
	require.NoError(t, os.MkdirAll(paths.Long(filepath.Dir(p)), 0o700))
	require.NoError(t, os.WriteFile(paths.Long(p), line, 0o600))
	return p
}

// spooledObserveTool returns the request a hook's own client would have spooled for sess.
func spooledObserveTool(root string, sess core.SessionID) ipc.Request {
	return ipc.Request{
		Op: ipc.OpObserveTool, Session: sess, TS: 1,
		Event: &hookio.Event{HookEventName: "PostToolUse", SessionID: sess, CWD: root, ToolName: "Read"},
	}
}

// TestRedrainOnFirstServedRequest is V2-MERGE-25 ①'s regression test: an entry spooled AFTER the
// startup drain has already scanned the spool directory must not have to wait for an idle tick.
//
// The two spool files are what make this deterministic rather than merely probable. The first is
// written before Run and is consumed by Run's own startup Drain; its disappearance is proof that
// the startup drain has finished its (already snapshotted) file list, so the second file — written
// only after that — provably cannot be seen by it. Nothing then drains the second file except the
// re-drain that the single served admin.ping releases (daemon.go, redrainOnceServing), and the
// bound this test waits under is far below the 30s idle-tick fallback that would otherwise be the
// answer.
//
// Before the fix the second file sat undrained until that idle tick, which is exactly how
// TestE2ELazySpawn came to fail only on a loaded machine.
func TestRedrainOnFirstServedRequest(t *testing.T) {
	root := t.TempDir()
	t.Setenv("QOMPACK_IPC_ADDR", uniqueTestAddr(t))

	observed := make(chan core.SessionID, 4)
	var o Options
	o.ProjectRoot = root
	o.Cfg = testConfig()
	o.Log = logging.Nop()
	o.Clock = core.SystemClock()
	o.Bind(func(s *Services) {
		s.ObserveTool = func(_ context.Context, e hookio.Event) error {
			observed <- e.SessionID
			return nil
		}
	})

	const startupSess = core.SessionID("sess-startup-drain")
	const redrainSess = core.SessionID("sess-re-drain")
	startupPath := writeClientSpoolLine(t, root, "client-11111.ndjson", spooledObserveTool(root, startupSess))

	d, err := New(o)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- d.Run(ctx) }()

	require.Eventually(t, func() bool {
		_, statErr := os.Stat(paths.Long(startupPath))
		return os.IsNotExist(statErr)
	}, drainDeadlockGuard, redrainTestTick, "Run's startup drain never consumed the pre-existing spool file")

	// Written only now: strictly after the startup drain's own file list was taken, which is the
	// window V2-MERGE-25 describes and the one the idle tick used to own alone.
	redrainPath := writeClientSpoolLine(t, root, "client-22222.ndjson", spooledObserveTool(root, redrainSess))

	addr, err := ipc.Resolve(root)
	require.NoError(t, err)
	c := ipc.NewClientWithOptions(addr, nil, logging.Nop(), nil, ipc.ClientOptions{
		State:           ipc.State{Mode: contract.ModeFull, DaemonEnabled: true},
		ConnectDeadline: redrainDialBound,
		AckDeadline:     redrainDialBound,
	})
	defer func() { _ = c.Close() }()

	// A nil SpoolWriter is deliberate: a Send that cannot reach the daemon yet must DROP rather
	// than spool, or the retries below would litter the very directory this test is watching.
	// The first Send that comes back OK is, by construction, the first request the daemon served.
	require.Eventually(t, func() bool {
		resp, sendErr := c.Send(context.Background(), ipc.Request{
			Op: ipc.OpAdminPing, TS: core.NowMilli(core.SystemClock()), Reply: true,
		}, redrainTestBound)
		return sendErr == nil && resp.OK
	}, drainDeadlockGuard, redrainTestTick, "the daemon never answered admin.ping, so nothing ever proved Serve was up")

	require.Eventually(t, func() bool {
		_, statErr := os.Stat(paths.Long(redrainPath))
		return os.IsNotExist(statErr)
	}, redrainTestBound, redrainTestTick,
		"a spool entry written after the startup drain was not re-drained within %s — only the %s idle tick would have taken it",
		redrainTestBound, idleTickMax)

	var got []core.SessionID
	for len(got) < 2 {
		select {
		case s := <-observed:
			got = append(got, s)
		case <-time.After(drainDeadlockGuard):
			t.Fatalf("only %d of the 2 spooled observe.tool events were ever dispatched: %v", len(got), got)
		}
	}
	require.ElementsMatch(t, []core.SessionID{startupSess, redrainSess}, got,
		"both drains must dispatch their line, not merely delete the file")

	cancel()
	select {
	case <-errCh:
	case <-time.After(drainDeadlockGuard):
		t.Fatal("Run did not shut down after cancellation")
	}
}

// TestSpoolOnBreachFalseDoesNotTransition: spoolOnBreach=false leaves the daemon in HotSync even
// after a breaching window, while the WARN log and counter still fire.
func TestSpoolOnBreachFalseDoesNotTransition(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cfg := testConfig()
	cfg.Runtime.HotPath.SpoolOnBreach = false
	cfg.Runtime.HotPath.BudgetMs = 15
	cfg.Runtime.HotPath.BreachWindows = 1
	d, err := New(Options{ProjectRoot: root, Cfg: cfg, Log: logging.Nop()})
	require.NoError(t, err)
	dd, ok := d.(*daemon)
	require.True(t, ok)
	t.Cleanup(func() { _ = dd.ing.Close() })

	feedBreachingWindow(dd)

	require.Equal(t, ipc.HotSync, dd.registry.HotMode())
	require.Equal(t, int64(1), dd.m.Counter(counterHotpathDegraded).Value())
}

// TestHotModeTransitionWritesStateAndNAKs is V2-SP05-15's four-way visibility requirement in full:
// a breaching window must make the sync -> spool transition visible in state.bin, in the NAK frame,
// in the WARN/LOUD log AND in status. It asserted only the first two, so a regression that flipped
// the mode without telling anyone — the operator reading logs, /qompack:status reading the
// snapshot — passed. §8.1 calls this fallback "observable"; two of the four channels were not
// being checked.
//
// The logger here is a real one rather than logging.Nop precisely because the log line is one of
// the four things under test.
func TestHotModeTransitionWritesStateAndNAKs(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	logDir := paths.Of(root).Logs
	log, closer, err := logging.New(logDir, logging.Warn)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, closer.Close()) })

	cfg := testConfig()
	cfg.Runtime.HotPath.BudgetMs = 15
	cfg.Runtime.HotPath.BreachWindows = 1
	d, err := New(Options{ProjectRoot: root, Cfg: cfg, Log: log})
	require.NoError(t, err)
	dd, ok := d.(*daemon)
	require.True(t, ok)
	t.Cleanup(func() { _ = dd.ing.Close() })

	// In production run/ always exists before any state.bin write — AcquireLock creates it during
	// Run's startup, long before a hot-path sample can be taken. This test drives the transition
	// directly, so it states that precondition rather than leaning on the accident that used to
	// supply it: with no .qompack/ on disk at all, paths.WriteAtomic cannot resolve a project root
	// and stages into the destination's OWN directory, creating run/ as a side effect. The moment
	// anything else creates .qompack/ first — logging.New, below, makes .qompack/logs — WriteAtomic
	// resolves the root properly and stages into .qompack/tmp/ instead, and nothing creates run/.
	// TestConfigReloadDefersChunkChange states the same precondition for the same reason.
	require.NoError(t, os.MkdirAll(paths.Long(paths.Of(root).Run), 0o700))

	feedBreachingWindow(dd)
	require.Equal(t, ipc.HotSpool, dd.registry.HotMode())

	// 1. state.bin — what every client reads before it decides whether to dial at all.
	st := ipc.ReadState(root, cfg)
	require.Equal(t, ipc.HotSpool, st.Hot)

	// 2. the NAK frame — the in-band hint that flips an already-running client to spool submode.
	ev := &hookio.Event{HookEventName: "PostToolUse", SessionID: "sess-1", CWD: root}
	resp := dd.dispatchOp(context.Background(), ipc.Request{Op: ipc.OpObserveTool, Session: "sess-1", Event: ev, TS: core.NowMilli(dd.clk)})
	require.False(t, resp.OK)
	require.Equal(t, ipc.HotSpool, resp.Hot)

	// 3. the log — the only channel that survives the process, and so the only one an operator can
	// read after the fact. applyHotPathTransition emits both a Warn and a Loud; LOUD.log is
	// append-only and never rotated, which is what makes it the durable half.
	loud, readErr := os.ReadFile(filepath.Join(logDir, "LOUD.log"))
	require.NoError(t, readErr, "the transition must reach LOUD.log")
	require.Contains(t, string(loud), "hot path degraded to spool submode")
	require.Contains(t, readDayLogs(t, logDir), "hot path degraded to spool submode",
		"the transition must also reach the day log, at WARN")

	// 4. status — what /qompack:status renders, and the only channel a user can query on demand.
	statusResp := dd.dispatchOp(context.Background(), ipc.Request{Op: ipc.OpStatus, Session: "sess-1", Reply: true, TS: core.NowMilli(dd.clk)})
	require.True(t, statusResp.OK, "status must answer while degraded; statusResp.Err=%q", statusResp.Err)
	var snap StatusSnapshot
	require.NoError(t, json.Unmarshal(statusResp.Data, &snap))
	require.Equal(t, "spool", snap.Hot, "StatusSnapshot.Hot must report the transition, not the submode the daemon started in")
}

// readDayLogs returns the concatenated contents of every qompack-<day>.log in dir. The day log's
// name carries a date, so a test that wants to read what it just wrote globs rather than
// reconstructing the filename from a clock it does not control.
func readDayLogs(t *testing.T, dir string) string {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(dir, "qompack-*.log"))
	require.NoError(t, err)
	require.NotEmpty(t, paths, "no day log was written in %s", dir)
	var all strings.Builder
	for _, p := range paths {
		b, readErr := os.ReadFile(p)
		require.NoError(t, readErr)
		all.Write(b)
	}
	return all.String()
}

// feedBreachingWindow pushes one full sampleWindow of 20ms samples (over a 15ms budget) directly
// through recordHotPathSample/breach.Observe/applyHotPathTransition — no worker goroutine is
// running in these tests, so the window is closed synchronously by draining d.hotSamples inline.
// TS=1/recvTS=21 (rather than TS=0) keeps the sample valid under validHotPathTS's ts<=0 rejection
// while still producing the intended 20ms "observed" latency.
func feedBreachingWindow(dd *daemon) {
	for i := 0; i < sampleWindow; i++ {
		dd.recordHotPathSample(ipc.Request{Op: ipc.OpObserveTool, TS: 1}, core.UnixMilli(21))
	}
	for len(dd.hotSamples) > 0 {
		s := <-dd.hotSamples
		if tr, _ := dd.breach.Observe(s); tr != NoTransition {
			dd.applyHotPathTransition(tr)
		}
	}
}

// TestConfigReloadDefersChunkChange: a store.chunk.* change in config.json is picked up on
// reload but deferred — the live config keeps the old chunk block, and the full new config is
// recorded to state/config-pending.json.
func TestConfigReloadDefersChunkChange(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cfg := testConfig()
	d, err := New(Options{ProjectRoot: root, Cfg: cfg, Log: logging.Nop()})
	require.NoError(t, err)
	dd, ok := d.(*daemon)
	require.True(t, ok)
	t.Cleanup(func() { _ = dd.ing.Close() })
	dd.cfgEnv = config.Env{ProjectRoot: root, HomeDir: t.TempDir(), Getenv: func(string) string { return "" }}

	// In production, .qompack/run/ always exists by the time reload can fire — every real call
	// site (Run's idle tick, session.start, admin.reload) runs only after AcquireLock has already
	// created it. This test drives reloadConfig directly, without Run, so it recreates that
	// precondition explicitly rather than asserting on an unrealistic one.
	require.NoError(t, os.MkdirAll(paths.Long(paths.Of(root).Run), 0o700))

	cfgPath := configJSONPath(root)
	require.NoError(t, os.MkdirAll(filepath.Dir(cfgPath), 0o700))
	body := []byte(`{"store":{"chunk":{"min":1024,"target":9999,"max":16384}},` +
		`"runtime":{"logging":{"level":"debug"},"daemon":{"ackDeadlineMs":99}}}`)
	require.NoError(t, os.WriteFile(cfgPath, body, 0o600))

	changed, err := dd.reloadConfig(context.Background(), dd.cfgEnv, false)
	require.NoError(t, err)
	require.NotEmpty(t, changed)
	require.Equal(t, cfg.Store.Chunk, dd.currentCfg().Store.Chunk, "store.chunk.* must not apply mid-session")

	b, err := os.ReadFile(paths.Long(configPendingPath(root)))
	require.NoError(t, err)
	var pending config.Config
	require.NoError(t, json.Unmarshal(b, &pending))
	require.Equal(t, 9999, pending.Store.Chunk.Target)

	// fix round 1, I-4: reload must rewrite state.bin, since it is the one thing every client
	// reads at construction — a reloaded runtime.daemon.ackDeadlineMs must reach it.
	st := ipc.ReadState(root, cfg)
	require.EqualValues(t, 99, st.AckDeadlineMs, "state.bin must reflect the reloaded config")
}

// TestIdleExitWithZeroSessions: with a 1-second idle-exit window and zero live sessions, Run
// returns on its own, having removed state.bin and released the lock.
func TestIdleExitWithZeroSessions(t *testing.T) {
	root := t.TempDir()
	t.Setenv("QOMPACK_IPC_ADDR", uniqueTestAddr(t))

	cfg := testConfig()
	cfg.Runtime.Daemon.IdleExitSeconds = 1
	d, err := New(Options{ProjectRoot: root, Cfg: cfg, Log: logging.Nop(), Clock: core.SystemClock()})
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	errCh := make(chan error, 1)
	go func() { errCh <- d.Run(ctx) }()

	select {
	case err := <-errCh:
		require.NoError(t, err)
	case <-time.After(12 * time.Second):
		t.Fatal("Run did not exit on its own after the idle-exit window")
	}

	_, statErr := os.Stat(paths.Long(ipc.StatePath(root)))
	require.True(t, os.IsNotExist(statErr), "state.bin must be removed on idle exit")
	_, lockErr := os.Stat(paths.Long(filepath.Join(paths.Of(root).Run, lockFileName)))
	require.True(t, os.IsNotExist(lockErr), "daemon.lock must be released on idle exit")
}

// TestRunReturnsNilWhenLockHeld: a second daemon over the same project returns nil promptly,
// exactly like a successful, quiet no-op.
func TestRunReturnsNilWhenLockHeld(t *testing.T) {
	root := t.TempDir()
	t.Setenv("QOMPACK_IPC_ADDR", uniqueTestAddr(t))

	cfg := testConfig()
	dA, err := New(Options{ProjectRoot: root, Cfg: cfg, Log: logging.Nop(), Clock: core.SystemClock()})
	require.NoError(t, err)
	ctxA, cancelA := context.WithCancel(context.Background())
	defer cancelA()
	errChA := make(chan error, 1)
	go func() { errChA <- dA.Run(ctxA) }()

	require.Eventually(t, func() bool {
		_, ok := ReadLock(root)
		return ok
	}, 5*time.Second, 20*time.Millisecond, "daemon A must acquire the lock")

	dB, err := New(Options{ProjectRoot: root, Cfg: cfg, Log: logging.Nop(), Clock: core.SystemClock()})
	require.NoError(t, err)
	ctxB, cancelB := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelB()

	require.NoError(t, dB.Run(ctxB), "a second daemon over the same project must return nil, not an error")

	cancelA()
	select {
	case <-errChA:
	case <-time.After(5 * time.Second):
		t.Fatal("daemon A did not shut down")
	}
}

// TestAdminShutdownStopsTheDaemon: the admin.shutdown route's asynchronous Stop() call must
// actually unblock Run — it must cancel the same context the worker pool, hot-path worker and
// server.Serve run under, or Stop's own d.ing.Wait() (and Run's own select loop) would hang
// forever waiting for a shutdown nothing ever signals.
func TestAdminShutdownStopsTheDaemon(t *testing.T) {
	root := t.TempDir()
	t.Setenv("QOMPACK_IPC_ADDR", uniqueTestAddr(t))

	d, err := New(Options{ProjectRoot: root, Cfg: testConfig(), Log: logging.Nop(), Clock: core.SystemClock()})
	require.NoError(t, err)
	dd, ok := d.(*daemon)
	require.True(t, ok)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- d.Run(ctx) }()

	require.Eventually(t, func() bool {
		_, ok := ReadLock(root)
		return ok
	}, 5*time.Second, 20*time.Millisecond, "the daemon must acquire the lock before it can be asked to shut down")

	resp := dd.dispatchOp(context.Background(), ipc.Request{Op: ipc.OpAdminShutdown, Reply: true})
	require.True(t, resp.OK)

	select {
	case err := <-errCh:
		require.NoError(t, err)
	case <-time.After(8 * time.Second):
		t.Fatal("admin.shutdown did not stop the running daemon")
	}

	// Run returning only proves runCtx was cancelled — it races ahead of admin.shutdown's own
	// `go func(){ Stop() }()` goroutine, which closes d.stopped (Run's own cue to return) BEFORE
	// running its actual cleanup (drain, ingest close, sketches save, metrics.Persist — which
	// writes .qompack/metrics/latency.json via paths.WriteAtomic, briefly creating a temp file in
	// .qompack/tmp/ — state removal, server close, lock release). Without waiting for stopDone
	// here too, t.TempDir()'s own cleanup can observe .qompack/tmp/ mid-write and fail with
	// "directory is not empty" (shutdown-race fix, intermittent on Windows).
	select {
	case <-dd.stopDone:
	case <-time.After(8 * time.Second):
		t.Fatal("Stop's cleanup did not finish")
	}
}

// TestServeFailureTakesTheStopPath closes FR-4's one untested arm (§2.5a G).
//
// Run's select has two ways out of `case err := <-serveErrCh`. A Serve return with d.stopped
// already closed is an intentional shutdown and reports nil. A Serve return with d.stopped still
// OPEN is a transport failure nobody asked for, and it must get the same Stop the ctx.Done arm
// gets: without it daemon.lock stays held until process death, state.bin goes on advertising a
// dead daemon, and the ingest WAL, sketches and metrics never flush.
//
// SP-05 shipped that arm with no dedicated test by accepted adjudication — a deterministic
// transport failure needs ipc-layer injection — and recorded that a verifier can drive it directly
// with server.Close(). This is that, from inside the package: nothing calls Stop, nothing cancels
// the context, the server is simply closed out from under the running daemon.
//
// Run's own return value is nil here, and that is the arm behaving correctly rather than a weak
// assertion. ipc.Server.Serve returns nil on a Close-driven shutdown by its own documented
// contract, and this arm reports whatever Serve returned VERBATIM rather than inventing an error
// of its own — deliberately, so a real failure is never shadowed. What proves the arm ran is the
// cleanup: Stop finished, and it finished without anyone having called it.
func TestServeFailureTakesTheStopPath(t *testing.T) {
	root := t.TempDir()
	t.Setenv("QOMPACK_IPC_ADDR", uniqueTestAddr(t))

	d, err := New(Options{ProjectRoot: root, Cfg: testConfig(), Log: logging.Nop(), Clock: core.SystemClock()})
	require.NoError(t, err)
	dd, ok := d.(*daemon)
	require.True(t, ok)

	// Deliberately NOT cancelled on the happy path: a cancelled context would let Run leave via
	// the ctx.Done arm instead, which is a different arm that already has its own tests.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- d.Run(ctx) }()

	// Both artifacts must exist before they can meaningfully be asserted gone, and dd.server must
	// be set before it can be closed — Run assigns it, writes state.bin and takes the lock in that
	// order, so the lock is the last of the three to appear.
	statePath := paths.Long(ipc.StatePath(root))
	lockPath := paths.Long(filepath.Join(paths.Of(root).Run, lockFileName))
	require.Eventually(t, func() bool {
		if _, lockOK := ReadLock(root); !lockOK {
			return false
		}
		_, statErr := os.Stat(statePath)
		return statErr == nil && dd.server != nil
	}, drainDeadlockGuard, redrainTestTick, "the daemon never finished starting, so there was nothing to fail")

	// The transport dies under a daemon that believes itself healthy.
	require.NoError(t, dd.server.Close())

	select {
	case runErr := <-errCh:
		require.NoError(t, runErr, "a Close-driven Serve return is nil by ipc.Server's contract, and this arm reports it verbatim")
	case <-time.After(drainDeadlockGuard):
		t.Fatal("Run never returned after its server was closed out from under it — the FR-4 arm is missing or wedged")
	}

	select {
	case <-dd.stopDone:
	case <-time.After(drainDeadlockGuard):
		t.Fatal("Run returned but Stop's cleanup never finished — the arm returned without taking the Stop path")
	}

	_, statErr := os.Stat(statePath)
	require.True(t, os.IsNotExist(statErr),
		"state.bin must be removed: left behind, every client keeps dialling an endpoint no daemon is on")
	_, lockErr := os.Stat(lockPath)
	require.True(t, os.IsNotExist(lockErr),
		"daemon.lock must be released: held by a dead daemon, no replacement can ever take this project")
}

// TestMarkerIsWrittenByFlushAndCheckpointOnly is the daemon-route half of task-4-spec.md's
// contract-side guarantee: session.start never writes run/marker.json; checkpoint and flush both
// do.
func TestMarkerIsWrittenByFlushAndCheckpointOnly(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	d, err := New(Options{ProjectRoot: root, Cfg: testConfig(), Log: logging.Nop()})
	require.NoError(t, err)
	dd, ok := d.(*daemon)
	require.True(t, ok)
	t.Cleanup(func() { _ = dd.ing.Close() })

	markerPath := contract.MarkerPath(root)
	_, err = os.Stat(paths.Long(markerPath))
	require.True(t, os.IsNotExist(err))

	ev := &hookio.Event{HookEventName: "SessionStart", SessionID: "sess-1", CWD: root, Source: "startup"}
	resp := dd.dispatchOp(context.Background(), ipc.Request{Op: ipc.OpSessionStart, Session: "sess-1", Reply: true, Event: ev})
	require.True(t, resp.OK)
	_, err = os.Stat(paths.Long(markerPath))
	require.True(t, os.IsNotExist(err), "session.start must never write the marker")

	ev2 := &hookio.Event{HookEventName: "PreCompact", SessionID: "sess-1", CWD: root}
	resp2 := dd.dispatchOp(context.Background(), ipc.Request{Op: ipc.OpCheckpoint, Session: "sess-1", Reply: true, Event: ev2})
	require.True(t, resp2.OK)
	_, err = os.Stat(paths.Long(markerPath))
	require.NoError(t, err, "checkpoint must write the marker")

	require.NoError(t, os.Remove(paths.Long(markerPath)))
	ev3 := &hookio.Event{HookEventName: "SessionEnd", SessionID: "sess-1", CWD: root}
	resp3 := dd.dispatchOp(context.Background(), ipc.Request{Op: ipc.OpFlush, Session: "sess-1", Reply: true, Event: ev3})
	require.True(t, resp3.OK)
	_, err = os.Stat(paths.Long(markerPath))
	require.NoError(t, err, "flush must write the marker")
}

// TestSessionStartNeverWipesSentinelObserved is the fix round 1 I-2 regression:
// SentinelState.Observed is documented (contract/history.go) as "never resets to false" once a
// scan has proven the mechanism works, and RecordSentinelScan is careful never to clear it. A
// session.start that mints a fresh token must respect that — assigning the whole SentinelState
// struct, as an earlier revision did, silently wiped it on every session that may act.
func TestSessionStartNeverWipesSentinelObserved(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	d, err := New(Options{ProjectRoot: root, Cfg: testConfig(), Log: logging.Nop()})
	require.NoError(t, err)
	dd, ok := d.(*daemon)
	require.True(t, ok)
	t.Cleanup(func() { _ = dd.ing.Close() })

	// Seed history as if a prior session's sentinel scan had already proven the mechanism works.
	h := contract.LoadHistory(contract.HistoryPath(root))
	h.Sentinel = contract.SentinelState{Token: "qompack-contract-old", Session: "sess-0", Observed: true, Chances: 1}
	require.NoError(t, contract.SaveHistory(contract.HistoryPath(root), h))

	ev := &hookio.Event{HookEventName: "SessionStart", SessionID: "sess-1", CWD: root, Source: "startup"}
	resp := dd.dispatchOp(context.Background(), ipc.Request{Op: ipc.OpSessionStart, Session: "sess-1", Reply: true, Event: ev})
	require.True(t, resp.OK)

	after := contract.LoadHistory(contract.HistoryPath(root))
	require.True(t, after.Sentinel.Observed, "Observed must never reset to false")
	require.NotEqual(t, "qompack-contract-old", after.Sentinel.Token, "a fresh token is still minted")
	require.Equal(t, core.SessionID("sess-1"), after.Sentinel.Session)
	require.Equal(t, 0, after.Sentinel.Chances, "a freshly minted token starts its own Chances count")
}

// TestSessionStartResetsBreachDetectorForNewSession is the fix round 1 I-7/I-8 regression:
// breachDetector.Reset() must actually be called somewhere, or a partially-filled breaching
// window from a previous, unrelated session closes on a brand-new session's very first request.
func TestSessionStartResetsBreachDetectorForNewSession(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cfg := testConfig()
	cfg.Runtime.HotPath.BudgetMs = 15
	cfg.Runtime.HotPath.BreachWindows = 1
	d, err := New(Options{ProjectRoot: root, Cfg: cfg, Log: logging.Nop()})
	require.NoError(t, err)
	dd, ok := d.(*daemon)
	require.True(t, ok)
	t.Cleanup(func() { _ = dd.ing.Close() })

	// Fill the ring most of the way with breaching samples — NOT a full window, so nothing has
	// transitioned yet — leaving dd.breach.n == sampleWindow-1, dirty.
	for i := 0; i < sampleWindow-1; i++ {
		tr, closed := dd.breach.Observe(20 * time.Millisecond)
		require.Equal(t, NoTransition, tr)
		require.False(t, closed)
	}

	// A brand-new session's session.start must reset the detector before its own traffic can
	// close a window on the previous session's leftover backlog.
	ev := &hookio.Event{HookEventName: "SessionStart", SessionID: "sess-new", CWD: root, Source: "startup"}
	resp := dd.dispatchOp(context.Background(), ipc.Request{Op: ipc.OpSessionStart, Session: "sess-new", Reply: true, Event: ev})
	require.True(t, resp.OK)

	// One more sample after the reset must NOT close a window — it would have, had the
	// pre-existing sampleWindow-1 backlog survived the reset.
	tr, closed := dd.breach.Observe(20 * time.Millisecond)
	require.Equal(t, NoTransition, tr)
	require.False(t, closed, "session.start must have reset the ring; one sample alone must never close a window")
}

// TestRecordHotPathSampleRejectsInvalidTS is the fix round 1 I-6 regression: ipc.DecodeRequest
// performs no validation of its own, so an absent or zero "t" must never be allowed to feed the
// B-A histograms or the breach detector as if it were a real, ~55-year-old sample.
func TestRecordHotPathSampleRejectsInvalidTS(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	d, err := New(Options{ProjectRoot: root, Cfg: testConfig(), Log: logging.Nop()})
	require.NoError(t, err)
	dd, ok := d.(*daemon)
	require.True(t, ok)
	t.Cleanup(func() { _ = dd.ing.Close() })

	cases := []struct {
		name string
		ts   core.UnixMilli
	}{
		{"zero", 0},
		{"negative", -1000},
		{"absurd-past", 1}, // recvTS - 1 is ~55 years given a realistic recvTS below
	}
	recvTS := core.NowMilli(dd.clk)
	for _, c := range cases {
		dd.recordHotPathSample(ipc.Request{Op: ipc.OpObserveTool, TS: c.ts}, recvTS)
	}
	require.Empty(t, dd.hotSamples, "no invalid sample may reach the breach-detector channel")
	require.Equal(t, int64(len(cases)), dd.m.Counter(counterHotpathSampleInvalid).Value())

	// A genuinely valid, recent TS is still accepted.
	dd.recordHotPathSample(ipc.Request{Op: ipc.OpObserveTool, TS: recvTS - 5}, recvTS)
	require.Len(t, dd.hotSamples, 1)
}

// TestRunCtxDoneGoesThroughStop is the fix round 1 I-7 regression: cancelling the caller's OWN
// ctx (as opposed to the idle-exit path, which already called Stop) must still flush sketches,
// close the WAL, persist metrics, remove state.bin and release the lock — not return a bare nil.
func TestRunCtxDoneGoesThroughStop(t *testing.T) {
	root := t.TempDir()
	t.Setenv("QOMPACK_IPC_ADDR", uniqueTestAddr(t))

	d, err := New(Options{ProjectRoot: root, Cfg: testConfig(), Log: logging.Nop(), Clock: core.SystemClock()})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- d.Run(ctx) }()

	require.Eventually(t, func() bool {
		_, ok := ReadLock(root)
		return ok
	}, 5*time.Second, 20*time.Millisecond, "the daemon must acquire the lock before it can be cancelled")

	cancel()
	select {
	case err := <-errCh:
		require.NoError(t, err)
	case <-time.After(8 * time.Second):
		t.Fatal("Run did not return after ctx cancellation")
	}

	_, statErr := os.Stat(paths.Long(ipc.StatePath(root)))
	require.True(t, os.IsNotExist(statErr), "ctx.Done must go through Stop, which removes state.bin")
	_, lockErr := os.Stat(paths.Long(filepath.Join(paths.Of(root).Run, lockFileName)))
	require.True(t, os.IsNotExist(lockErr), "ctx.Done must go through Stop, which releases daemon.lock")
}

// TestAdminPingAlwaysAnswersOK pins Ruling #22/#1: admin.ping answers OK:true unconditionally —
// the ACK path's liveness proof depends on Response.OK, so this cannot be allowed to ever refuse.
func TestAdminPingAlwaysAnswersOK(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	d, err := New(Options{ProjectRoot: root, Cfg: testConfig(), Log: logging.Nop()})
	require.NoError(t, err)
	dd, ok := d.(*daemon)
	require.True(t, ok)
	t.Cleanup(func() { _ = dd.ing.Close() })
	dd.monitor.Degrade("forced for test", []contract.Result{{ID: "x", OK: false, Severity: contract.SevCritical}})

	resp := dd.dispatchOp(context.Background(), ipc.Request{Op: ipc.OpAdminPing, Reply: true})
	require.True(t, resp.OK, "admin.ping must answer OK:true even while degraded")
	require.NotNil(t, resp.Data)

	var data map[string]any
	require.NoError(t, json.Unmarshal(resp.Data, &data))
	require.Contains(t, data, "pid")
	require.Contains(t, data, "version")
	require.Contains(t, data, "uptime_seconds")
}

// TestUnknownOpIsRefusedNotPanicked pins the routing table's default case.
func TestUnknownOpIsRefusedNotPanicked(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	d, err := New(Options{ProjectRoot: root, Cfg: testConfig(), Log: logging.Nop()})
	require.NoError(t, err)
	dd, ok := d.(*daemon)
	require.True(t, ok)
	t.Cleanup(func() { _ = dd.ing.Close() })

	var resp ipc.Response
	require.NotPanics(t, func() {
		resp = dd.dispatchOp(context.Background(), ipc.Request{Op: "bogus.op", Reply: true})
	})
	require.False(t, resp.OK)
	require.Contains(t, resp.Err, "unknown op")
}

// TestHandlerPanicIsRecovered pins the belt-and-braces panic recovery around a route handler.
func TestHandlerPanicIsRecovered(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	o := Options{ProjectRoot: root, Cfg: testConfig(), Log: logging.Nop()}
	o.Handle(ipc.OpStatus, func(context.Context, ipc.Request) ipc.Response {
		panic("boom")
	})
	d, err := New(o)
	require.NoError(t, err)
	dd, ok := d.(*daemon)
	require.True(t, ok)
	t.Cleanup(func() { _ = dd.ing.Close() })

	var resp ipc.Response
	require.NotPanics(t, func() {
		resp = dd.dispatchOp(context.Background(), ipc.Request{Op: ipc.OpStatus, Reply: true})
	})
	require.False(t, resp.OK)
	require.Contains(t, resp.Err, "panic")
	require.Equal(t, int64(1), dd.m.Counter(counterHandlerPanic).Value())
}

// TestIdleExitEndsAbandonedSessions: a client that dies without ever sending SessionEnd must not
// hold the daemon open forever. Before the abandonment sweep, this exact shape -- one session
// Ensured and then silent -- kept Live() at 1 indefinitely, the zero-live countdown never
// started, and the daemon outlived its project until process death (V2-VERIFY's ci-local run
// left three such orphans holding their temp dirs for hours). With the sweep, the session is
// abandoned after one idle-exit window of silence and the daemon exits after one more.
func TestIdleExitEndsAbandonedSessions(t *testing.T) {
	root := t.TempDir()
	t.Setenv("QOMPACK_IPC_ADDR", uniqueTestAddr(t))

	cfg := testConfig()
	cfg.Runtime.Daemon.IdleExitSeconds = 1
	d, err := New(Options{ProjectRoot: root, Cfg: cfg, Log: logging.Nop(), Clock: core.SystemClock()})
	require.NoError(t, err)

	// The abandoned session: registered the way an accepted hook registers it, never ended.
	d.Registry().Ensure(&hookio.Event{SessionID: "sess-vanished"}, core.NowMilli(core.SystemClock()))
	require.Equal(t, 1, d.Registry().Live())

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	errCh := make(chan error, 1)
	go func() { errCh <- d.Run(ctx) }()

	select {
	case err := <-errCh:
		require.NoError(t, err)
	case <-time.After(12 * time.Second):
		t.Fatal("Run did not exit on its own: the abandoned session still counts as live")
	}

	require.Zero(t, d.Registry().Live(), "the vanished session must have been ended, not kept live")
}

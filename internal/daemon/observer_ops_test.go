// TestWireObserver asserts the L0 wiring THROUGH A BUILT DAEMON rather than against the Options
// value, which is what catches the WireObserver(o Options) value form: Bind appends to o.binds,
// and nothing is observable until New runs those binds. It is the daemon-side test V3-VERIFY H12
// re-runs.
package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/contract"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/negknow"
	"github.com/qompack/qompack/internal/observer"
	"github.com/qompack/qompack/internal/store"
)

// fakeStalenessLedger records RefreshStaleness calls. It embeds negknow.Ledger so the nine
// methods the wired SessionStart seam never calls need no body.
type fakeStalenessLedger struct {
	negknow.Ledger

	mu    sync.Mutex
	calls int
	err   error
}

func (l *fakeStalenessLedger) RefreshStaleness(_ context.Context, _ store.Store) ([]string, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls++
	return nil, l.err
}

func (l *fakeStalenessLedger) count() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.calls
}

func (l *fakeStalenessLedger) setErr(err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.err = err
}

// wireTestDaemon runs WireObserver over a fresh NewOptions, mutates the Options, builds the real
// daemon, and registers the cleanups every test here needs (the store WireObserver opened holds
// append handles that would otherwise fail t.TempDir's cleanup on Windows).
func wireTestDaemon(t *testing.T, root string, mutate func(*Options)) (observer.Observer, *daemon, *Options) {
	t.Helper()
	o := NewOptions(root, testConfig())
	if mutate != nil {
		mutate(&o)
	}
	obsv, err := WireObserver(&o)
	require.NoError(t, err)
	require.NotNil(t, obsv)
	require.NotNil(t, o.Store, "WireObserver must open the store when the field is nil")
	require.NotNil(t, o.Graph, "WireObserver must open the DAG when the field is nil")
	require.NotNil(t, o.Sketches)
	t.Cleanup(func() { _ = o.Store.Close() })

	d, err := New(o)
	require.NoError(t, err)
	dd, ok := d.(*daemon)
	require.True(t, ok)
	t.Cleanup(func() { _ = dd.ing.Close() })
	return obsv, dd, &o
}

func TestWireObserver(t *testing.T) {
	root := t.TempDir()
	obsv, dd, o := wireTestDaemon(t, root, nil)

	// The daemon's Services carry the SAME instances WireObserver assigned back onto Options.
	require.Same(t, o.Store, dd.svc.Store)
	require.Same(t, o.Graph, dd.svc.Graph)
	require.Same(t, o.Sketches, dd.svc.Sketches)

	// All five L0 seams are non-nil on the daemon built from the wired Options.
	require.NotNil(t, dd.svc.ObserveTool)
	require.NotNil(t, dd.svc.ObservePrompt)
	require.NotNil(t, dd.svc.ObserveStop)
	require.NotNil(t, dd.svc.SessionStart)
	require.NotNil(t, dd.svc.SessionEnd)

	// The mode seam round-trips: a fresh project's monitor reports ModeFull, and the mapping the
	// observer consumes preserves it while sending both degraded states to ModePassive.
	require.NotNil(t, dd.svc.Mode)
	require.Equal(t, contract.ModeFull, dd.svc.Mode())
	require.Equal(t, observer.ModeFull, mapContractMode(contract.ModeFull))
	require.Equal(t, observer.ModePassive, mapContractMode(contract.ModeDegradedPassive))
	require.Equal(t, observer.ModePassive, mapContractMode(contract.ModeOff))

	// RegisterObserverIdleWork registers observer.persist at priority 50: two marker tasks at 49
	// and 51 pin its position in the priority-sorted run order.
	RegisterObserverIdleWork(dd, obsv)
	noop := func(context.Context) error { return nil }
	dd.idle.Register("test.before", observerPersistPrio-1, noop)
	dd.idle.Register("test.after", observerPersistPrio+1, noop)
	names := dd.idle.Registered()
	idx := func(name string) int {
		for i, n := range names {
			if n == name {
				return i
			}
		}
		return -1
	}
	require.GreaterOrEqual(t, idx("observer.persist"), 0, "observer.persist must be registered: %v", names)
	require.Less(t, idx("test.before"), idx("observer.persist"), "run order %v", names)
	require.Less(t, idx("observer.persist"), idx("test.after"), "run order %v", names)

	// A driven observe.tool request reaches the observer through SP-05's route: dispatchOp WALs it
	// and the worker pool hands it to the bound seam, which records into the SAME store instance.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	dd.ing.Start(ctx, 2, dd.runIngested)
	ev := &hookio.Event{
		HookEventName: "PostToolUse", SessionID: "sess-wire", CWD: root, ToolName: "Read",
		ToolUseID: "toolu_wire_1", ToolInput: json.RawMessage(`{"file_path":"src/a.go"}`),
		ToolResponse: json.RawMessage(`{"content":"package a\n"}`),
	}
	resp := dd.dispatchOp(context.Background(), ipc.Request{
		Op: ipc.OpObserveTool, Session: "sess-wire", Event: ev, TS: core.NowMilli(dd.clk),
	})
	require.True(t, resp.OK)
	require.Eventually(t, func() bool {
		_, err := o.Store.ToolUse(context.Background(), "toolu_wire_1")
		return err == nil
	}, 5*time.Second, 10*time.Millisecond,
		"the driven observe.tool never reached the observer: no index record appeared")
}

func TestWireObserver_SessionStartRefreshesStaleness(t *testing.T) {
	root := t.TempDir()
	led := &fakeStalenessLedger{}
	_, dd, _ := wireTestDaemon(t, root, func(o *Options) { o.Ledger = led })

	startup := hookio.Event{HookEventName: "SessionStart", SessionID: "sess-stale", CWD: root, Source: "startup"}
	_, err := dd.svc.SessionStart(context.Background(), startup)
	require.NoError(t, err)
	require.Equal(t, 1, led.count(), "exactly one RefreshStaleness call on startup")

	// compact and clear are the rehydrator's branches: the refresh is gated off them.
	for _, source := range []string{"compact", "clear"} {
		ev := startup
		ev.Source = source
		_, err = dd.svc.SessionStart(context.Background(), ev)
		require.NoError(t, err)
	}
	require.Equal(t, 1, led.count(), "compact/clear must not refresh staleness")

	// A refresh failure is a Warn, never a hook failure.
	led.setErr(errors.New("stale boom"))
	_, err = dd.svc.SessionStart(context.Background(), startup)
	require.NoError(t, err, "a RefreshStaleness error must never fail the hook")
	require.Equal(t, 2, led.count())

	// A nil Ledger — the ordinary stub build — must neither panic nor error.
	_, dd2, _ := wireTestDaemon(t, t.TempDir(), nil)
	require.NotPanics(t, func() {
		_, err := dd2.svc.SessionStart(context.Background(), startup)
		require.NoError(t, err)
	})
}

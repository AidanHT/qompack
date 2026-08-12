package daemon_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/daemon"
	"github.com/qompack/qompack/internal/ipc"
)

// TestNew_SucceedsWithNoServices is the property waves 1–2 depend on: a daemon constructs from an
// Options whose service members are all nil. §5.4 says nil means "not built yet", so refusing to
// construct would make the daemon unusable during exactly the period it is most needed.
func TestNew_SucceedsWithNoServices(t *testing.T) {
	t.Parallel()

	d, err := daemon.New(daemon.Options{ProjectRoot: t.TempDir()})
	require.NoError(t, err)
	require.NotNil(t, d)

	// The two extension seams must be usable even before Run works, because they are what later
	// waves register against at wiring time.
	require.NotNil(t, d.Registry(), "Registry is an extension seam and must never be nil")
	require.NotNil(t, d.Idle(), "Idle is an extension seam and must never be nil")
}

// TestStubOperationsReportNotImplemented pins the stub contract: every operation reports
// core.ErrNotImplemented, so a caller can branch on it rather than guessing from a zero value.
func TestStubOperationsReportNotImplemented(t *testing.T) {
	t.Parallel()

	d, err := daemon.New(daemon.Options{ProjectRoot: t.TempDir()})
	require.NoError(t, err)
	ctx := context.Background()

	require.True(t, core.IsNotImplemented(d.Run(ctx)))
	require.True(t, core.IsNotImplemented(d.Stop(ctx)))

	n, err := d.Drain(ctx)
	require.True(t, core.IsNotImplemented(err))
	require.Zero(t, n)

	ran, err := d.Idle().RunOnce(ctx, 0)
	require.True(t, core.IsNotImplemented(err))
	require.Empty(t, ran)
}

// TestHandle_RoutingTableIsData is the seam §5.4 calls out by name.
//
// The op-routing table has to be data rather than a switch, because otherwise every wave that
// adds an op edits the same function in this package — which is how four wave-3 subplans end up
// conflicting in one file. This test is what keeps it data.
func TestHandle_RoutingTableIsData(t *testing.T) {
	t.Parallel()

	var o daemon.Options

	_, ok := o.Handler("observe.tool")
	require.False(t, ok, "an unregistered op must not resolve")

	called := 0
	o.Handle("observe.tool", func(context.Context, ipc.Request) ipc.Response {
		called++
		return ipc.Response{OK: true}
	})
	o.Handle("checkpoint", func(context.Context, ipc.Request) ipc.Response {
		return ipc.Response{OK: true}
	})

	h, ok := o.Handler("observe.tool")
	require.True(t, ok)
	resp := h(context.Background(), ipc.Request{})
	require.True(t, resp.OK)
	require.Equal(t, 1, called)

	require.ElementsMatch(t, []ipc.Op{"observe.tool", "checkpoint"}, o.Ops())

	// Re-registering replaces rather than duplicating, so a later wave can override an earlier
	// wave's placeholder without the table growing two entries for one op.
	o.Handle("observe.tool", func(context.Context, ipc.Request) ipc.Response {
		return ipc.Response{OK: false}
	})
	require.Len(t, o.Ops(), 2)
	h, _ = o.Handler("observe.tool")
	require.False(t, h(context.Background(), ipc.Request{}).OK)
	require.Equal(t, 1, called, "the replaced handler must not still be reachable")
}

// TestIdleController_AcceptsRegistrationsBeforeItCanRun proves the O3/O5 seam is wired now even
// though nothing executes yet: a wave that registers background work today must be able to see
// that its registration landed, or it cannot test its own wiring until SP-05 merges.
func TestIdleController_AcceptsRegistrationsBeforeItCanRun(t *testing.T) {
	t.Parallel()

	d, err := daemon.New(daemon.Options{ProjectRoot: t.TempDir()})
	require.NoError(t, err)

	idle := d.Idle()
	idle.Register("gc", 10, func(context.Context) error { return nil })
	idle.Register("persist-sketches", 20, func(context.Context) error { return nil })

	reg, ok := idle.(interface{ Registered() []string })
	require.True(t, ok, "the stub controller must expose what was registered")
	require.ElementsMatch(t, []string{"gc", "persist-sketches"}, reg.Registered())

	// IsIdle is false unconditionally and that is the documented answer, not an oversight:
	// reporting idle while no work can run would invite a caller to schedule work that silently
	// never happens.
	idle.Notify(core.UnixMilli(0))
	require.False(t, idle.IsIdle(core.UnixMilli(1<<40)))
}

// TestSessionRegistry_EmptyAndSafe covers the registry's real behaviour. It is real rather than
// stubbed because it is touched by the accept loop and the idle loop concurrently by
// construction; shipping it unsynchronized would leave SP-05 a data race to find under -race
// instead of a correct skeleton to fill in.
func TestSessionRegistry_EmptyAndSafe(t *testing.T) {
	t.Parallel()

	r := daemon.NewSessionRegistry()
	require.Zero(t, r.Len())

	_, ok := r.Get("nobody")
	require.False(t, ok)
}

// TestSessionRegistry_ConcurrentReadsAreRaceFree exercises the lock under -race.
func TestSessionRegistry_ConcurrentReadsAreRaceFree(t *testing.T) {
	t.Parallel()

	r := daemon.NewSessionRegistry()
	done := make(chan struct{})
	const readers = 8

	for i := 0; i < readers; i++ {
		go func() {
			defer func() { done <- struct{}{} }()
			for j := 0; j < 100; j++ {
				_ = r.Len()
				_, _ = r.Get("s1")
			}
		}()
	}
	for i := 0; i < readers; i++ {
		<-done
	}
}

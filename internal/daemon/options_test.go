package daemon

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/contract"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/ipc"
)

// TestNewOptions_SeedsSaneDefaults pins NewOptions' seams: a working logger, metrics registry and
// clock all sharing one Clock instance, and a SketchSet sized from cfg.
func TestNewOptions_SeedsSaneDefaults(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	o := NewOptions(root, testConfig())
	require.Equal(t, root, o.ProjectRoot)
	require.NotNil(t, o.Log)
	require.NotNil(t, o.Metrics)
	require.NotNil(t, o.Clock)
	require.NotNil(t, o.Sketches)
}

// TestHandleOnBareOptionsIsSafe: a bare Options{} literal never panics on Handle, and New still
// succeeds against it (this package's seams are safe by construction — there is no
// ErrOptionsUninitialized in this codebase's real shape, per the SP-05 realities that supersede
// the historical spec text).
func TestHandleOnBareOptionsIsSafe(t *testing.T) {
	t.Parallel()

	var o Options
	require.NotPanics(t, func() {
		o.Handle(ipc.OpStatus, func(context.Context, ipc.Request) ipc.Response {
			return ipc.Response{OK: true}
		})
	})
	h, ok := o.Handler(ipc.OpStatus)
	require.True(t, ok)
	require.True(t, h(context.Background(), ipc.Request{}).OK)

	o.ProjectRoot = t.TempDir()
	d, err := New(o)
	require.NoError(t, err)
	require.NotNil(t, d)
}

// TestHandleOverridesDefaultRoute: opts.Handle(OpStatus, custom) before New makes the custom
// handler run instead of the default status route.
func TestHandleOverridesDefaultRoute(t *testing.T) {
	t.Parallel()

	o := NewOptions(t.TempDir(), testConfig())
	called := false
	o.Handle(ipc.OpStatus, func(context.Context, ipc.Request) ipc.Response {
		called = true
		return ipc.Response{OK: true, Err: "custom"}
	})

	d, err := New(o)
	require.NoError(t, err)
	dd, ok := d.(*daemon)
	require.True(t, ok)

	resp := dd.dispatchOp(context.Background(), ipc.Request{Op: ipc.OpStatus, Reply: true})
	require.True(t, called)
	require.Equal(t, "custom", resp.Err)
}

// TestBindRunsInOrderAndDeclaresProducers: two Binds run in registration order, and binding
// Rehydrate declares CAdditionalContext's producer (the four-of-nine gated seams).
func TestBindRunsInOrderAndDeclaresProducers(t *testing.T) {
	contract.ResetProducers()
	t.Cleanup(contract.ResetProducers)

	o := NewOptions(t.TempDir(), testConfig())
	var order []string
	o.Bind(func(s *Services) { order = append(order, "first") })
	o.Bind(func(s *Services) {
		order = append(order, "second")
		s.Rehydrate = func(context.Context, hookio.Event) (hookio.Output, error) {
			return hookio.Empty(), nil
		}
	})

	_, err := New(o)
	require.NoError(t, err)
	require.Equal(t, []string{"first", "second"}, order)
	require.True(t, contract.HasProducer(contract.CAdditionalContext))
}

// TestDeclaredProducerSetMatchesArchitecture pins the exact five-always/four-gated split
// options.go's DeclareProducers implements (§12.1: "exactly these four, no more and no fewer").
func TestDeclaredProducerSetMatchesArchitecture(t *testing.T) {
	contract.ResetProducers()
	t.Cleanup(contract.ResetProducers)

	// Nothing bound: only the five always-declared producers exist.
	DeclareProducers(&Services{})
	require.True(t, contract.HasProducer(contract.CSessionStartFires))
	require.True(t, contract.HasProducer(contract.CSessionStartSourceCompact))
	require.True(t, contract.HasProducer(contract.CHookPayloadShape))
	require.True(t, contract.HasProducer(contract.CTranscriptReadable))
	require.True(t, contract.HasProducer(contract.CPluginRootResolves))
	require.False(t, contract.HasProducer(contract.CPreCompactTiming))
	require.False(t, contract.HasProducer(contract.CPreCompactCustomInstr))
	require.False(t, contract.HasProducer(contract.CAdditionalContext))
	require.False(t, contract.HasProducer(contract.CMCPRegistered))

	contract.ResetProducers()
	DeclareProducers(&Services{
		PreCompact:     func(context.Context, hookio.Event) (hookio.Output, error) { return hookio.Output{}, nil },
		Rehydrate:      func(context.Context, hookio.Event) (hookio.Output, error) { return hookio.Output{}, nil },
		MCPInitialized: func(context.Context) bool { return true },
	})
	require.True(t, contract.HasProducer(contract.CPreCompactTiming))
	require.True(t, contract.HasProducer(contract.CPreCompactCustomInstr))
	require.True(t, contract.HasProducer(contract.CAdditionalContext))
	require.True(t, contract.HasProducer(contract.CMCPRegistered))

	// Checkpoints alone (no PreCompact seam) also gates the two PreCompact-timing assertions.
	contract.ResetProducers()
	DeclareProducers(&Services{Checkpoints: nil})
	require.False(t, contract.HasProducer(contract.CPreCompactTiming))
}

// TestServicesFromNeverReturnsNil: a context with nothing bound still yields a usable *Services.
func TestServicesFromNeverReturnsNil(t *testing.T) {
	t.Parallel()
	s := ServicesFrom(context.Background())
	require.NotNil(t, s)
	require.Nil(t, s.ObserveTool)
}

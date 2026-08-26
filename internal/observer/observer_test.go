package observer

import (
	"context"
	"testing"
	"time"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/logging"
	"github.com/stretchr/testify/require"
)

// validOptions is the minimal Options New must accept: the five collaborators without which L0
// cannot record anything at all, and nothing else (resolved decision 8).
func validOptions(t *testing.T) Options {
	t.Helper()
	return Options{
		ProjectRoot: t.TempDir(),
		Cfg:         config.Defaults(),
		Store:       newFakeStore(),
		Graph:       newFakeGraph(),
		Log:         logging.Nop(),
		Clock:       newFakeClock(),
	}
}

func TestNew_RequiresItsMandatoryCollaborators(t *testing.T) {
	for name, breakIt := range map[string]func(*Options){
		"ProjectRoot": func(o *Options) { o.ProjectRoot = "" },
		"Store":       func(o *Options) { o.Store = nil },
		"Graph":       func(o *Options) { o.Graph = nil },
		"Log":         func(o *Options) { o.Log = nil },
		"Clock":       func(o *Options) { o.Clock = nil },
	} {
		t.Run(name, func(t *testing.T) {
			o := validOptions(t)
			breakIt(&o)
			built, err := New(o)
			require.Error(t, err, "a nil %s must be reported, not discovered at the first hook", name)
			require.Nil(t, built)
		})
	}
}

func TestNew_AcceptsEveryOptionalCollaboratorAsNil(t *testing.T) {
	built, err := New(validOptions(t))
	require.NoError(t, err)
	require.NotNil(t, built)

	_, ok := built.(Persister)
	require.True(t, ok, "the value New returns ALWAYS satisfies Persister")
}

func TestNew_MaxResultBytesFallsBackToTheDefault(t *testing.T) {
	t.Run("absent", func(t *testing.T) {
		o := validOptions(t)
		o.Cfg = config.Config{} // no runtime section at all
		built, err := New(o)
		require.NoError(t, err)
		impl, ok := built.(*observer)
		require.True(t, ok)
		require.Equal(t, config.Defaults().Runtime.HotPath.MaxPayloadBytes, impl.maxResultBytes,
			"the fallback is the DEFAULT, never a literal")
	})

	t.Run("configured", func(t *testing.T) {
		o := validOptions(t)
		o.Cfg.Runtime.HotPath.MaxPayloadBytes = 7777
		built, err := New(o)
		require.NoError(t, err)
		impl, ok := built.(*observer)
		require.True(t, ok)
		require.Equal(t, 7777, impl.maxResultBytes)
	})
}

func TestObserver_ModeDefaultsToFull(t *testing.T) {
	h := newHarness(t)
	require.Equal(t, ModeFull, h.obs.mode(), "a nil Options.Mode is full operation")

	passive := newHarness(t, func(o *Options) { o.Mode = func() Mode { return ModePassive } })
	require.Equal(t, ModePassive, passive.obs.mode())
}

func TestObserver_SessionIsCreatedOnceWithNonNilMaps(t *testing.T) {
	h := newHarness(t)

	first := h.obs.session("sess_x")
	require.NotNil(t, first.TodoDone)
	require.NotNil(t, first.WarnedRules)
	require.Same(t, first, h.obs.session("sess_x"), "the map is the only accessor and it memoizes")
	require.NotSame(t, first, h.obs.session("sess_y"))
}

func TestObserver_MetricsAreOptional(t *testing.T) {
	h := newHarness(t, func(o *Options) { o.Metrics = nil })

	require.NotPanics(t, func() {
		h.drive(readOf("toolu_1", "src/a.ts", "alpha\n"))
		h.obs.count("observer.err.anything")
		require.NoError(t, h.obs.timed("observer.tooluse", func() error { return nil }))
	})
}

func TestObserver_SoftIgnoresANilError(t *testing.T) {
	h := newHarness(t)

	h.obs.soft(stagePut, nil)

	require.Equal(t, int64(0), h.counter("observer.err."+stagePut),
		"a stage that succeeded must not bump an error counter")
}

func TestObserver_NowTruncatesToUnixMilli(t *testing.T) {
	h := newHarness(t)

	before := h.obs.now()
	h.Clock.Advance(500 * time.Microsecond)
	require.Equal(t, before, h.obs.now(), "a sub-millisecond advance does not move a UnixMilli")

	h.Clock.Advance(time.Second)
	require.Equal(t, before+1000, h.obs.now())
}

func TestObserver_EveryEntryPointReturnsCtxErr(t *testing.T) {
	h := newHarness(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var e Event
	for name, call := range map[string]func() (Output, error){
		"OnToolUse":      func() (Output, error) { return h.obs.OnToolUse(ctx, e) },
		"OnUserPrompt":   func() (Output, error) { return h.obs.OnUserPrompt(ctx, e) },
		"OnStop":         func() (Output, error) { return h.obs.OnStop(ctx, e, false) },
		"OnSessionStart": func() (Output, error) { return h.obs.OnSessionStart(ctx, e) },
		"OnSessionEnd":   func() (Output, error) { return h.obs.OnSessionEnd(ctx, e) },
	} {
		t.Run(name, func(t *testing.T) {
			_, err := call()
			require.ErrorIs(t, err, context.Canceled)
		})
	}
}

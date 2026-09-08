package daemon

// The plan's 24 scheduler_runtime_test.go cases (plans/V4-SP-12-scheduler-l3.md, "Test plan")
// plus TestResolveWindow_HostVariables (the seat notes' §2.5 host-variable ladder) and one
// snapshot check. Every runtime is built over the in-memory doubles of
// scheduler_testhelpers_test.go, the in-package fake clock, and a t.TempDir() project root, so
// paths.WriteAtomic and the state files are real and nothing here reads the wall clock.
//
// Two cases — TestRuntime_ObserveClosesSegmentOnChangepoint and
// TestRuntime_OpenSegmentTokensResetOnClose — exercise closeSegmentLocked, which seat C2 supplies
// in scheduler_frontier.go. Against C1's one-line stub they FAIL by design; they go green when C2
// lands. TestRuntime_ObserveNoOpWhenAdvanceOnSegmentCloseFalse passes vacuously against the stub
// and becomes a real assertion at the same moment.
//
// The three tests that assert scheduler.PSelectionAvailable() (a process-wide atomic every
// runtime construction flips) run sequentially — no t.Parallel — so no parallel sibling can
// toggle the gate underneath them; every other case is parallel.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/dag"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/negknow"
	"github.com/qompack/qompack/internal/obs"
	"github.com/qompack/qompack/internal/scheduler"
	"github.com/qompack/qompack/internal/store"
)

// ── Series fixture (transcribed from internal/scheduler/bocd_test.go, which this package cannot
// import) ──────────────────────────────────────────────────────────────────────────────────

const rtSeriesSeed uint32 = 0x5EED_1234

const (
	rtSeriesNoise         = 0.05
	rtSeriesBaseMean      = 0.2
	rtGapReferenceSeconds = 600.0
	rtStepShift           = 0.6
)

// rtLCG is bocd_test.go's 32-bit LCG (Numerical Recipes constants): bit-identical everywhere.
type rtLCG struct{ state uint32 }

func (g *rtLCG) next() float64 {
	g.state = g.state*1664525 + 1013904223
	return float64(g.state>>8) / float64(1<<24)
}

func rtClamp01(x float64) float64 { return min(max(x, 0), 1) }

// rtFeaturesAt builds a Features whose every mapped stream equals v under the detector's
// orientation (PathJaccard and LexicalCohesion inverted, GapSeconds un-compressed).
func rtFeaturesAt(v float64) scheduler.Features {
	return scheduler.Features{
		PathJaccard:     1 - v,
		ToolShift:       v,
		LexicalCohesion: 1 - v,
		GapSeconds:      math.Expm1(v * math.Log1p(rtGapReferenceSeconds)),
		TodoTransition:  v,
	}
}

// rtStepSeries is bocd_test.go's stepSeries: n observations at mean 0.2, then mean 0.2+shift
// after n/2, with ±0.05 uniform noise from the fixed seed.
func rtStepSeries(n int, shift float64) []scheduler.Features {
	g := &rtLCG{state: rtSeriesSeed}
	out := make([]scheduler.Features, n)
	for i := range out {
		mean := rtSeriesBaseMean
		if i >= n/2 {
			mean += shift
		}
		out[i] = rtFeaturesAt(rtClamp01(mean + (g.next()-0.5)*2*rtSeriesNoise))
	}
	return out
}

// ── Runtime fixture ─────────────────────────────────────────────────────────────────────────

const rtSession core.SessionID = "sess-c1"

// rtFixture bundles the doubles one scheduler runtime test drives.
type rtFixture struct {
	root    string
	clock   *fakeClock
	store   *fakeStore
	graph   *fakeGraph
	log     *recordingLogger
	reg     obs.Registry
	env     map[string]string
	cfg     config.Config
	writer  *fakeWriter
	session core.SessionID
	// ledgerFn is the live-ledger supplier the composition root passes; nil in most fixtures.
	ledgerFn func() negknow.Ledger
	rt       *schedRuntime
}

func (fx *rtFixture) options() SchedulerRuntimeOptions {
	env := fx.env
	o := SchedulerRuntimeOptions{
		ProjectRoot: fx.root,
		Session:     fx.session,
		Cfg:         fx.cfg,
		Clock:       fx.clock,
		Log:         fx.log,
		Metrics:     fx.reg,
		Store:       fx.store,
		Graph:       fx.graph,
		Getenv:      func(k string) string { return env[k] },
	}
	if fx.writer != nil {
		o.Checkpoints = fx.writer
	}
	o.LedgerFn = fx.ledgerFn
	return o
}

// newRTFixture builds a runtime over fresh doubles, applying mods to the fixture first. The
// p-selection gate the constructor enables is released at cleanup.
func newRTFixture(t testing.TB, mods ...func(*rtFixture)) *rtFixture {
	t.Helper()
	fx := &rtFixture{
		root:  t.TempDir(),
		clock: newFakeClock(epoch),
		store: newFakeStore(),
		graph: newFakeGraph(),
		log:   newRecordingLogger(),
		env:   map[string]string{},
		cfg:   config.Defaults(),
	}
	fx.reg = obs.New(fx.clock)
	for _, m := range mods {
		m(fx)
	}
	rt, err := NewSchedulerRuntime(fx.options())
	require.NoError(t, err)
	t.Cleanup(scheduler.DisablePSelection)
	concrete, ok := rt.(*schedRuntime)
	require.True(t, ok, "NewSchedulerRuntime returns the daemon's concrete runtime")
	fx.rt = concrete
	return fx
}

// withRoot shares a project root, its store and graph, and the clock with an earlier fixture:
// what a daemon restart sees (the same project, the same segment log, a new process).
func withRoot(prev *rtFixture) func(*rtFixture) {
	return func(fx *rtFixture) {
		fx.root = prev.root
		fx.store = prev.store
		fx.graph = prev.graph
		fx.clock = prev.clock
		fx.reg = obs.New(fx.clock)
	}
}

func (fx *rtFixture) bind(id core.SessionID) { fx.rt.BindSession(id, nil) }

func (fx *rtFixture) now() core.UnixMilli { return core.NowMilli(fx.clock) }

func (fx *rtFixture) counter(name string) int64 { return fx.reg.Counter(name).Value() }

// feed observes series at turns 1..len(series) and returns every state the detector produced.
func (fx *rtFixture) feed(series []scheduler.Features) []scheduler.ChangepointState {
	out := make([]scheduler.ChangepointState, len(series))
	for i, f := range series {
		out[i] = fx.rt.Observe(context.Background(), f, core.TurnIndex(i+1))
	}
	return out
}

// declaredTurns lists the 1-based turns at which states declared a changepoint.
func declaredTurns(states []scheduler.ChangepointState) []core.TurnIndex {
	var out []core.TurnIndex
	for i, s := range states {
		if s.AtChangepoint {
			out = append(out, core.TurnIndex(i+1))
		}
	}
	return out
}

func (fx *rtFixture) statePath(name string) string {
	return filepath.Join(fx.root, ".qompack", "state", name)
}

func (fx *rtFixture) evaluate(t *testing.T) scheduler.Decision {
	t.Helper()
	d, err := fx.rt.Evaluate(context.Background())
	require.NoError(t, err)
	return d
}

// ── Constructor ─────────────────────────────────────────────────────────────────────────────

func TestNewSchedulerRuntime_RequiresDeps(t *testing.T) {
	scheduler.DisablePSelection()
	defer scheduler.DisablePSelection()
	fx := &rtFixture{
		root: t.TempDir(), clock: newFakeClock(epoch), store: newFakeStore(), graph: newFakeGraph(),
		log: newRecordingLogger(), env: map[string]string{}, cfg: config.Defaults(),
	}
	fx.reg = obs.New(fx.clock)
	cases := []struct {
		name string
		omit func(o *SchedulerRuntimeOptions)
	}{
		{"store", func(o *SchedulerRuntimeOptions) { o.Store = nil }},
		{"graph", func(o *SchedulerRuntimeOptions) { o.Graph = nil }},
		{"clock", func(o *SchedulerRuntimeOptions) { o.Clock = nil }},
		{"log", func(o *SchedulerRuntimeOptions) { o.Log = nil }},
		{"project root", func(o *SchedulerRuntimeOptions) { o.ProjectRoot = "" }},
	}
	for _, tc := range cases {
		o := fx.options()
		tc.omit(&o)
		rt, err := NewSchedulerRuntime(o)
		require.Error(t, err, tc.name)
		require.Nil(t, rt, tc.name)
		require.ErrorContains(t, err, "daemon: scheduler runtime: "+tc.name+" required")
		require.False(t, scheduler.PSelectionAvailable(), "the gate must stay closed after %s is missing", tc.name)
	}
}

func TestNewSchedulerRuntime_EnablesPSelectionGate(t *testing.T) {
	scheduler.DisablePSelection()
	defer scheduler.DisablePSelection()
	fx := newRTFixture(t)
	require.True(t, scheduler.PSelectionAvailable(), "construction is the closing-note-3 unlock")
	require.Equal(t, int64(1), fx.counter(counterPSelectionEnabled))
	require.NoError(t, CloseSchedulerRuntime(fx.rt))
	require.False(t, scheduler.PSelectionAvailable(), "Close must release the gate")
	require.NoError(t, CloseSchedulerRuntime(nil), "nil is tolerated")
	require.NoError(t, CloseSchedulerRuntime(fakeForeignRuntime{}), "a foreign Runtime is tolerated")
}

// TestRuntime_CloseThenRebindSameIDReopensGate is the exit-then-`--resume` path with the daemon
// still up: SessionEnd closed the runtime (gate released), the same session id binds again.
// Sequential: it asserts the process-wide gate.
func TestRuntime_CloseThenRebindSameIDReopensGate(t *testing.T) {
	scheduler.DisablePSelection()
	defer scheduler.DisablePSelection()
	fx := newRTFixture(t)
	first := &hookio.Event{SessionID: rtSession, Source: "startup", Extra: map[string]json.RawMessage{
		"agent_id": json.RawMessage(`"researcher"`),
	}}
	fx.rt.BindSession(rtSession, first)
	require.True(t, scheduler.PSelectionAvailable())
	require.True(t, fx.rt.subagent)
	require.Equal(t, "subagent_5m", fx.rt.regime.Source)
	fx.store.segs.addSegment(t, rtSession, 1, 10, 5_000)
	fx.rt.NoteAPIRound(10)
	d := fx.evaluate(t)
	require.NotContains(t, d.Breakdown, "error_no_window")

	require.NoError(t, CloseSchedulerRuntime(fx.rt))
	require.False(t, scheduler.PSelectionAvailable(), "Close releases the gate")
	require.Equal(t, int64(1), fx.counter(counterPersist))

	fx.rt.BindSession(rtSession, &hookio.Event{SessionID: rtSession, Source: "resume"})
	require.True(t, scheduler.PSelectionAvailable(), "a same-id rebind after Close re-opens the gate")
	require.False(t, fx.rt.subagent, "agent_id absent on the binding event resets the subagent marker")
	require.Equal(t, "unknown", fx.rt.regime.Source)
	d = fx.evaluate(t)
	require.NotContains(t, d.Breakdown, "error_no_window", "the rebound runtime answers")
	require.Equal(t, 5_000.0, d.Breakdown["context_tokens"])
	require.Contains(t, fx.rt.rounds, core.TurnIndex(10), "the in-memory state Close persisted is kept")
	require.Equal(t, int64(1), fx.counter(counterPersist), "a same-id rebind reloads nothing and persists nothing")
	require.Zero(t, fx.log.count(logLoud))
}

// fakeForeignRuntime is a scheduler.Runtime that is not a *schedRuntime, for the helpers' failed
// type assertions.
type fakeForeignRuntime struct{}

func (fakeForeignRuntime) Observe(context.Context, scheduler.Features, core.TurnIndex) scheduler.ChangepointState {
	return scheduler.ChangepointState{}
}

func (fakeForeignRuntime) Evaluate(context.Context) (scheduler.Decision, error) {
	return scheduler.Decision{}, nil
}
func (fakeForeignRuntime) NotifyActivity(core.UnixMilli)     {}
func (fakeForeignRuntime) IdleSince() (core.UnixMilli, bool) { return 0, false }
func (fakeForeignRuntime) Persist(context.Context) error     { return nil }

func TestNewSchedulerRuntime_EmptySessionIsLegal(t *testing.T) {
	t.Parallel()
	fx := newRTFixture(t)
	require.Equal(t, core.SessionID(""), fx.rt.session)

	d := fx.evaluate(t)
	require.False(t, d.ShouldCompact)
	require.Equal(t, 1.0, d.Breakdown["error_no_window"], "no window before the first SessionStart")
	require.NotContains(t, d.Breakdown, "context_tokens")

	fx.bind("s1")
	d = fx.evaluate(t)
	require.NotContains(t, d.Breakdown, "error_no_window")
	require.Equal(t, float64(scheduler.EffectiveWindow(scheduler.HostDefaultContextWindow, scheduler.HostDefaultMaxOutput)),
		d.Breakdown["effective_window"])
	require.Equal(t, windowSourceHostDefault, d.Breakdown[breakdownWindowSource])
	require.Contains(t, fx.rt.rounds, core.TurnIndex(0), "turn 0 is always a legal cut point")
}

// ── Window ladder ───────────────────────────────────────────────────────────────────────────

func TestRuntime_WindowResolutionLadder(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		env    map[string]string
		window core.Tokens
		source float64
	}{
		{"autocompact", map[string]string{envAutoCompactWindow: "150000"}, 150_000, windowSourceAutoCompact},
		{"override", map[string]string{envContextWindow: "250000", envMaxOutputTokens: "8000"}, 242_000, windowSourceOverride},
		{"host_default", map[string]string{}, 180_000, windowSourceHostDefault},
		// Ruling R53: out-of-range rung-1 values are CLAMPED to [100 000, 1 000 000] and still
		// source 3; only unparsable, empty or non-positive values fall through.
		{"autocompact_below_range_clamped", map[string]string{envAutoCompactWindow: "50000"}, 100_000, windowSourceAutoCompact},
		{"autocompact_above_range_clamped", map[string]string{envAutoCompactWindow: "5000000", envContextWindow: "250000", envMaxOutputTokens: "8000"}, 1_000_000, windowSourceAutoCompact},
		{"autocompact_garbage_falls_through", map[string]string{envAutoCompactWindow: "abc", envContextWindow: "250000", envMaxOutputTokens: "8000"}, 242_000, windowSourceOverride},
		{"autocompact_empty_falls_through", map[string]string{envAutoCompactWindow: "  "}, 180_000, windowSourceHostDefault},
		{"autocompact_zero_falls_through", map[string]string{envAutoCompactWindow: "0"}, 180_000, windowSourceHostDefault},
		{"autocompact_negative_falls_through", map[string]string{envAutoCompactWindow: "-5"}, 180_000, windowSourceHostDefault},
		{"autocompact_outranks_override", map[string]string{envAutoCompactWindow: "150000", envContextWindow: "250000"}, 150_000, windowSourceAutoCompact},
		{"override_without_max_output_uses_host_default_output", map[string]string{envContextWindow: "250000"}, 230_000, windowSourceOverride},
		{"override_leaving_no_window_falls_through_to_host_default", map[string]string{envContextWindow: "1000", envMaxOutputTokens: "5000"}, 180_000, windowSourceHostDefault},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fx := newRTFixture(t, func(fx *rtFixture) { fx.env = tc.env })
			fx.bind(rtSession)
			require.Equal(t, tc.window, fx.rt.effectiveWindow)
			d := fx.evaluate(t)
			require.Equal(t, float64(tc.window), d.Breakdown["effective_window"])
			require.Equal(t, tc.source, d.Breakdown[breakdownWindowSource])
			if tc.source == windowSourceAutoCompact {
				require.Equal(t, core.Tokens(0), fx.rt.maxOutput, "rung 1 names the effective window directly")
			}
		})
	}
}

func TestResolveWindow_HostVariables(t *testing.T) {
	t.Parallel()

	// aboveCeiling puts n above the host-default hard ceiling (147 000) through closed segments.
	aboveCeiling := func(fx *rtFixture) {
		fx.store.segs.addSegment(t, rtSession, 1, 10, 150_000)
	}
	t.Run("disable_compact_caps_urgency_at_advisory", func(t *testing.T) {
		t.Parallel()
		fx := newRTFixture(t, aboveCeiling, func(fx *rtFixture) { fx.env[envDisableCompact] = "1" })
		fx.bind(rtSession)
		fx.rt.NoteAPIRound(10)
		require.True(t, fx.rt.hostTriggerAbsent)
		d := fx.evaluate(t)
		require.Equal(t, 1.0, d.Breakdown[breakdownHostCompactionDisabled])
		require.Contains(t, d.Reasons, scheduler.TriggerHardCeiling, "the clause still reports")
		require.Equal(t, scheduler.UrgencyAdvisory, d.Urgency, "no host trigger to stay ahead of")
		require.Equal(t, 1.0, d.Breakdown["urgency_capped_advisory"])
		require.Equal(t, 180_000.0, d.Breakdown["effective_window"], "the window itself is untouched")
	})
	t.Run("hard_ceiling_without_disable_compact_is_urgency_now", func(t *testing.T) {
		t.Parallel()
		fx := newRTFixture(t, aboveCeiling)
		fx.bind(rtSession)
		fx.rt.NoteAPIRound(10)
		d := fx.evaluate(t)
		require.Equal(t, scheduler.UrgencyNow, d.Urgency)
		require.NotContains(t, d.Breakdown, breakdownHostCompactionDisabled)
		require.NotContains(t, d.Breakdown, breakdownHostEnforcementOff)
		require.NotContains(t, d.Breakdown, breakdownWindowClamped200k)
	})
	t.Run("max_context_tokens_outranks_host_default", func(t *testing.T) {
		t.Parallel()
		fx := newRTFixture(t, func(fx *rtFixture) { fx.env[envMaxContextTokens] = "500000" })
		fx.bind(rtSession)
		require.Equal(t, core.Tokens(480_000), fx.rt.effectiveWindow, "500 000 minus the capped host output budget")
		require.Equal(t, scheduler.HostDefaultMaxOutput, fx.rt.maxOutput)
		require.Equal(t, windowSourceMaxContextTokens, fx.evaluate(t).Breakdown[breakdownWindowSource])
	})
	t.Run("max_context_tokens_does_not_outrank_autocompact", func(t *testing.T) {
		t.Parallel()
		fx := newRTFixture(t, func(fx *rtFixture) {
			fx.env[envMaxContextTokens] = "500000"
			fx.env[envAutoCompactWindow] = "150000"
		})
		fx.bind(rtSession)
		require.Equal(t, core.Tokens(150_000), fx.rt.effectiveWindow)
		require.Equal(t, windowSourceAutoCompact, fx.evaluate(t).Breakdown[breakdownWindowSource])
	})
	t.Run("max_context_tokens_is_clamped_to_range", func(t *testing.T) {
		t.Parallel()
		// Ruling R53: clamped to [100 000, 1 000 000] and still rung 2.5; only garbage falls through.
		cases := []struct {
			value  string
			window core.Tokens
			source float64
		}{
			{"50000", 100_000 - scheduler.HostMaxOutputCap, windowSourceMaxContextTokens},
			{"5000000", 1_000_000 - scheduler.HostMaxOutputCap, windowSourceMaxContextTokens},
			{"abc", 180_000, windowSourceHostDefault},
			{"0", 180_000, windowSourceHostDefault},
		}
		for _, tc := range cases {
			fx := newRTFixture(t, func(fx *rtFixture) { fx.env[envMaxContextTokens] = tc.value })
			fx.bind(rtSession)
			require.Equal(t, tc.window, fx.rt.effectiveWindow, "value %q", tc.value)
			require.Equal(t, tc.source, fx.evaluate(t).Breakdown[breakdownWindowSource], "value %q", tc.value)
		}
	})
	t.Run("disable_1m_context_without_a_clamp_stamps_nothing", func(t *testing.T) {
		t.Parallel()
		fx := newRTFixture(t, func(fx *rtFixture) { fx.env[envDisable1MContext] = "1" })
		fx.bind(rtSession)
		require.Equal(t, core.Tokens(180_000), fx.rt.effectiveWindow)
		require.NotContains(t, fx.evaluate(t).Breakdown, breakdownWindowClamped200k,
			"the host default is already at the 200K boundary: nothing was clamped, so nothing bound")
	})
	t.Run("disable_1m_context_clamps_after_every_rung", func(t *testing.T) {
		t.Parallel()
		fx := newRTFixture(t, func(fx *rtFixture) {
			fx.env[envMaxContextTokens] = "1000000"
			fx.env[envDisable1MContext] = "true"
		})
		fx.bind(rtSession)
		require.Equal(t, core.Tokens(180_000), fx.rt.effectiveWindow, "clamped to the 200K boundary, then the output budget applies")
		d := fx.evaluate(t)
		require.Equal(t, 1.0, d.Breakdown[breakdownWindowClamped200k])
		require.Equal(t, windowSourceMaxContextTokens, d.Breakdown[breakdownWindowSource], "the source rung is still reported")
	})
	t.Run("disable_1m_context_clamps_the_autocompact_rung_too", func(t *testing.T) {
		t.Parallel()
		fx := newRTFixture(t, func(fx *rtFixture) {
			fx.env[envAutoCompactWindow] = "900000"
			fx.env[envDisable1MContext] = "yes"
		})
		fx.bind(rtSession)
		require.Equal(t, scheduler.HostDefaultContextWindow, fx.rt.effectiveWindow)
		require.Equal(t, 1.0, fx.evaluate(t).Breakdown[breakdownWindowClamped200k])
	})
	t.Run("unknown_model_enforcement_off_is_advisory_with_a_normal_window", func(t *testing.T) {
		t.Parallel()
		fx := newRTFixture(t, aboveCeiling, func(fx *rtFixture) { fx.env[envDisableUnknownModelEnforcement] = "1" })
		fx.bind(rtSession)
		fx.rt.NoteAPIRound(10)
		require.True(t, fx.rt.hostTriggerAbsent)
		d := fx.evaluate(t)
		require.Equal(t, 1.0, d.Breakdown[breakdownHostEnforcementOff])
		require.Equal(t, 180_000.0, d.Breakdown["effective_window"], "the window still resolves normally")
		require.Equal(t, scheduler.UrgencyAdvisory, d.Urgency)
	})
	t.Run("falsy_spellings_do_not_bind", func(t *testing.T) {
		t.Parallel()
		for _, v := range []string{"", "0", "false", "no", "off", "  OFF ", "False"} {
			fx := newRTFixture(t, aboveCeiling, func(fx *rtFixture) {
				fx.env[envDisableCompact] = v
				fx.env[envDisable1MContext] = v
				fx.env[envDisableUnknownModelEnforcement] = v
			})
			fx.bind(rtSession)
			fx.rt.NoteAPIRound(10)
			require.False(t, fx.rt.hostTriggerAbsent, "value %q", v)
			d := fx.evaluate(t)
			require.Equal(t, scheduler.UrgencyNow, d.Urgency, "value %q", v)
			require.NotContains(t, d.Breakdown, breakdownHostCompactionDisabled, "value %q", v)
			require.NotContains(t, d.Breakdown, breakdownWindowClamped200k, "value %q", v)
			require.NotContains(t, d.Breakdown, breakdownHostEnforcementOff, "value %q", v)
		}
	})
}

// ── Context tokens and segments ─────────────────────────────────────────────────────────────

func TestRuntime_ContextTokensFromSegments(t *testing.T) {
	t.Parallel()
	fx := newRTFixture(t)
	fx.bind(rtSession)
	fx.store.segs.addSegment(t, rtSession, 1, 10, 10_000)
	fx.store.segs.addSegment(t, rtSession, 11, 20, 25_000)
	fx.store.segs.addSegment(t, "someone-else", 1, 20, 999_999) // another session's history is not this context
	fx.rt.NoteAPIRound(20)
	fx.rt.AddOpenSegmentTokens(4_000)

	d := fx.evaluate(t)
	require.Equal(t, 39_000.0, d.Breakdown["context_tokens"], "closed segments plus the open accumulator")
	require.Equal(t, core.Tokens(39_000), fx.rt.contextTokens)

	fx.store.segs.rangeErr = errors.New("segment log offline")
	d = fx.evaluate(t)
	require.Equal(t, 4_000.0, d.Breakdown["context_tokens"], "Range failing ⇒ the open accumulator alone, no panic")
	require.NotEmpty(t, fx.log.msgs(logDebug))
	require.Zero(t, fx.log.count(logLoud))
}

// ── Observe ─────────────────────────────────────────────────────────────────────────────────

func TestRuntime_ObserveDeclaresAndRecordsChangepoint(t *testing.T) {
	t.Parallel()
	fx := newRTFixture(t)
	fx.bind(rtSession)
	states := fx.feed(rtStepSeries(200, rtStepShift))
	declared := declaredTurns(states)
	require.Len(t, declared, 1, "one 0.6 step ⇒ one declaration: %v", declared)
	require.GreaterOrEqual(t, int(declared[0]), 101)
	require.LessOrEqual(t, int(declared[0]), 106)
	require.Equal(t, declared, fx.rt.cpTurns)
	require.Equal(t, int64(1), fx.counter(counterChangepoint))
	require.Equal(t, core.TurnIndex(200), fx.rt.maxTurn)
	require.True(t, fx.rt.dirty, "a declaration must schedule a persist")
}

func TestRuntime_ObserveNoOpWhenAdvanceOnSegmentCloseFalse(t *testing.T) {
	t.Parallel()
	fx := newRTFixture(t, func(fx *rtFixture) { fx.cfg.Checkpoint.Frontier.AdvanceOnSegmentClose = false })
	fx.bind(rtSession)
	_, err := fx.store.segs.Open(context.Background(), store.Segment{Session: rtSession, StartTurn: 0})
	require.NoError(t, err)

	declared := declaredTurns(fx.feed(rtStepSeries(200, rtStepShift)))
	require.Len(t, declared, 1)
	require.Equal(t, declared, fx.rt.cpTurns, "the changepoint is still recorded")
	require.Empty(t, fx.store.segs.closeCalls)
	require.Len(t, fx.store.segs.openCalls, 1, "only the fixture's own open")
}

// ── Evaluate ────────────────────────────────────────────────────────────────────────────────

// rtToolUseFixture populates the store, graph and segment log with n tool-use records: record i
// lands at Pos i*tokensEach on turn 1+i/perTurn, with a KindToolResult node carrying the record
// id in Ref (as dag.BuildToolUse writes it). Turns are covered by closed segments of segTurns turns.
type rtToolUseFixture struct {
	records []store.ToolUseRecord
	pos     []int
	turns   int
}

func rtPopulateToolUses(t testing.TB, fx *rtFixture, n, perTurn int, tokensEach core.Tokens) rtToolUseFixture {
	t.Helper()
	tools := []string{"Read", "Bash", "Grep", "Task", "mcp__qompack__recall", "Edit"}
	out := rtToolUseFixture{records: make([]store.ToolUseRecord, n), pos: make([]int, n)}
	for i := range n {
		turn := core.TurnIndex(1 + i/perTurn)
		rec := store.ToolUseRecord{
			ID:        core.ToolUseID(fmt.Sprintf("toolu_%d", i)),
			Session:   rtSession,
			Turn:      turn,
			TS:        core.UnixMilli(epoch.UnixMilli()) + core.UnixMilli(i)*1_000,
			Tool:      tools[i%len(tools)],
			Tokens:    tokensEach,
			Ephemeral: i%10 == 0,
		}
		if i%8 == 0 {
			rec.Status = store.StatusSuperseded
		}
		fx.store.put(rec)
		pos := i * int(tokensEach)
		out.records[i] = rec
		out.pos[i] = pos
		require.NoError(t, fx.graph.AddNode(dag.Node{
			ID: dag.ToolUseNode(rec.ID), Kind: dag.KindToolUse, Turn: turn, TS: rec.TS, Pos: pos, Ref: rec.Tool,
		}))
		require.NoError(t, fx.graph.AddNode(dag.Node{
			ID: dag.ToolResultNode(rec.ID), Kind: dag.KindToolResult, Turn: turn, TS: rec.TS, Pos: pos,
			Ref: string(rec.ID), Tokens: rec.Tokens, Ephemeral: rec.Ephemeral,
		}))
		out.turns = int(turn)
	}
	for start := 1; start <= out.turns; start += segTurns {
		end := min(start+segTurns-1, out.turns)
		var tok core.Tokens
		for i := range n {
			if turn := 1 + i/perTurn; turn >= start && turn <= end {
				tok += tokensEach
			}
		}
		fx.store.segs.addSegment(t, rtSession, core.TurnIndex(start), core.TurnIndex(end), tok)
	}
	return out
}

// reclaimableAfter is the brute-force definition of reclaimable(p) over the fixture.
func (f rtToolUseFixture) reclaimableAfter(p int) core.Tokens {
	var sum core.Tokens
	for i, rec := range f.records {
		if f.pos[i] >= p && ClassifyDrop(rec) != DropNone {
			sum += rec.Tokens
		}
	}
	return sum
}

func TestRuntime_EvaluateProducesDecision(t *testing.T) {
	t.Parallel()
	const (
		toolUses   = 2_000
		perTurn    = 67
		tokensEach = 75
	)
	fx := newRTFixture(t)
	fx.bind(rtSession)
	tu := rtPopulateToolUses(t, fx, toolUses, perTurn, tokensEach)
	fx.rt.mu.Lock()
	for _, turn := range []core.TurnIndex{10, 20, 30} {
		fx.rt.recordChangepointLocked(turn)
	}
	fx.rt.mu.Unlock()
	fx.rt.NoteAPIRound(20)
	fx.rt.NoteAPIRound(core.TurnIndex(tu.turns + 1)) // advances maxTurn past every segment; not a changepoint turn

	d := fx.evaluate(t)

	// Hand-computed: n = 2 000 × 75 = 150 000 closed tokens > hard ceiling 147 000 (180 000 −
	// 13 000 − 20 000) > soft floor 99 000, so hard_ceiling fires with UrgencyNow. Of the three
	// changepoint turns (10, 20, 30) only turn 20 is a round boundary, so p is turn 20's first
	// position.
	require.Equal(t, 150_000.0, d.Breakdown["context_tokens"])
	require.True(t, d.ShouldCompact)
	require.Equal(t, scheduler.UrgencyNow, d.Urgency)
	require.Equal(t, []scheduler.TriggerReason{scheduler.TriggerSoftFloor, scheduler.TriggerHardCeiling}, d.Reasons)
	require.Equal(t, 3.0, d.Breakdown["candidates_supplied"])
	require.Equal(t, 1.0, d.Breakdown["candidates"], "turn 20 is the only round boundary among the changepoints")
	require.NotContains(t, d.Breakdown, "round_boundary_relaxed")
	require.Equal(t, core.TurnIndex(20), d.P.Turn)
	wantPos := 19 * perTurn * tokensEach
	require.Equal(t, wantPos, d.P.Pos)
	require.True(t, d.P.RoundBoundary)
	require.Equal(t, tu.reclaimableAfter(wantPos), d.P.ReclaimableTokens, "the suffix-sum index agrees with the brute-force sum")
	require.Equal(t, core.SegmentID(1+(20-1)/segTurns), d.P.SegmentID)
	require.NotEmpty(t, d.Breakdown)
	require.Equal(t, d, fx.rt.lastDecision)
	require.Equal(t, fx.now(), fx.rt.lastEvaluateTS)
}

func TestRuntime_EvaluateSuppliesLambdaFromSelectionConfig(t *testing.T) {
	t.Parallel()
	fx := newRTFixture(t, func(fx *rtFixture) { fx.cfg.Selection.Submodular.Lambda = 0.4 })
	fx.bind(rtSession)
	require.Equal(t, 0.4, fx.evaluate(t).Breakdown["lambda"])

	fx2 := newRTFixture(t, func(fx *rtFixture) { fx.cfg.Selection.Submodular.Lambda = 0.25 })
	fx2.bind(rtSession)
	require.Equal(t, 0.25, fx2.evaluate(t).Breakdown["lambda"])
}

func TestRuntime_DeltaPtrNilUntilMeasured(t *testing.T) {
	t.Parallel()
	fx := newRTFixture(t)
	fx.bind(rtSession)
	require.Nil(t, fx.rt.deltaPtr())
	d := fx.evaluate(t)
	require.Equal(t, 1.0, d.Breakdown["young_daly_delta_unmeasured"])

	fx.rt.RecordCompactionCost(20)
	require.NotNil(t, fx.rt.deltaPtr())
	d = fx.evaluate(t)
	require.NotContains(t, d.Breakdown, "young_daly_delta_unmeasured")
	require.Equal(t, 20.0, d.Breakdown["delta_seconds"])
	require.Equal(t, fx.now(), fx.rt.lastCompactionTS, "a measured compaction restarts the Young–Daly clock")
}

func TestRuntime_DeltaEWMA(t *testing.T) {
	t.Parallel()
	fx := newRTFixture(t)
	fx.rt.RecordCompactionCost(20)
	require.Equal(t, 20.0, fx.rt.deltaEWMA, "seeded on the first sample")
	require.Equal(t, 1, fx.rt.deltaSamples)
	fx.rt.RecordCompactionCost(30)
	require.InDelta(t, deltaEWMAAlpha*30+(1-deltaEWMAAlpha)*20, fx.rt.deltaEWMA, 1e-12)
	require.InDelta(t, 23.0, fx.rt.deltaEWMA, 1e-12)
	require.Equal(t, 2, fx.rt.deltaSamples)

	for _, bad := range []float64{0, -1, math.NaN(), math.Inf(1)} {
		fx.rt.RecordCompactionCost(bad)
	}
	require.Equal(t, 2, fx.rt.deltaSamples, "non-positive and non-finite samples are not measurements")
}

func TestRuntime_BurnRateEWMA(t *testing.T) {
	t.Parallel()
	fx := newRTFixture(t)
	fx.bind(rtSession)
	t0 := fx.now()
	fx.rt.NotifyActivity(t0) // baseline: no sample yet
	require.Equal(t, 0, fx.rt.burnSamples)

	fx.rt.AddOpenSegmentTokens(9_000)
	fx.rt.NotifyActivity(t0 + 60_000)
	require.Equal(t, 1, fx.rt.burnSamples)
	require.Equal(t, 9_000.0, fx.rt.burnEWMA, "first sample: 9 000 tokens over one minute")

	fx.rt.AddOpenSegmentTokens(3_000)
	fx.rt.NotifyActivity(t0 + 120_000)
	require.Equal(t, 2, fx.rt.burnSamples)
	require.InDelta(t, burnEWMAAlpha*3_000+(1-burnEWMAAlpha)*9_000, fx.rt.burnEWMA, 1e-9)

	fx.rt.NotifyActivity(t0 + 180_000)
	require.Equal(t, 2, fx.rt.burnSamples, "no tokens observed ⇒ no sample, and the baseline is kept")
	fx.rt.AddOpenSegmentTokens(6_000)
	fx.rt.NotifyActivity(t0 + 240_000)
	require.Equal(t, 3, fx.rt.burnSamples)
	require.InDelta(t, burnEWMAAlpha*(6_000/2.0)+(1-burnEWMAAlpha)*(burnEWMAAlpha*3_000+(1-burnEWMAAlpha)*9_000),
		fx.rt.burnEWMA, 1e-9, "6 000 tokens over the two minutes since the last sample")
	require.Equal(t, fx.rt.burnEWMA, fx.evaluate(t).Breakdown["burn_rate_tokens_per_min"])
}

func TestRuntime_NotifyActivitySetsAPICallClock(t *testing.T) {
	t.Parallel()
	fx := newRTFixture(t)
	_, idle := fx.rt.IdleSince()
	require.False(t, idle, "no activity yet")

	ts := fx.now()
	fx.rt.NotifyActivity(ts)
	require.Equal(t, ts, fx.rt.lastAPICallTS, "E1: the API-call clock is what the sliding TTL keys on")
	got, idle := fx.rt.IdleSince()
	require.Equal(t, ts, got)
	require.False(t, idle)

	fx.clock.Advance(time.Duration(fx.cfg.Scheduler.Idle.DetectAfterSeconds)*time.Second - time.Millisecond)
	_, idle = fx.rt.IdleSince()
	require.False(t, idle)
	fx.clock.Advance(time.Millisecond)
	got, idle = fx.rt.IdleSince()
	require.Equal(t, ts, got)
	require.True(t, idle)

	fx.rt.NotifyActivity(ts - 1)
	require.Equal(t, ts, fx.rt.lastActivity, "an older timestamp never rewinds the clock")
}

// ── Persistence ─────────────────────────────────────────────────────────────────────────────

// rtExercise drives a bound runtime through enough activity to populate every persisted field.
func rtExercise(t *testing.T, fx *rtFixture) {
	t.Helper()
	fx.feed(rtStepSeries(200, rtStepShift)) // declares once, around turn 101–106
	fx.rt.NoteAPIRound(3)
	fx.rt.NoteAPIRound(7)
	fx.rt.RecordCompactionCost(20)
	t0 := fx.now()
	fx.rt.NotifyActivity(t0)
	fx.rt.AddOpenSegmentTokens(4_000)
	// The clock advances with the stamps: a restart shares this clock (withRoot), and ruling
	// R59 clamps any restored stamp that is later than it.
	fx.clock.Advance(time.Minute)
	t1 := fx.now()
	fx.rt.NotifyActivity(t1)
	fx.rt.NoteRequestStart(t1)
	fx.rt.mu.Lock()
	fx.rt.frontier, fx.rt.residual, fx.rt.lastCheckpointSeq = 33, 9_120, 2
	fx.rt.mu.Unlock()
	fx.evaluate(t)
}

func TestRuntime_PersistRoundTrip(t *testing.T) {
	t.Parallel()
	a := newRTFixture(t)
	a.bind(rtSession)
	a.store.segs.addSegment(t, rtSession, 1, 20, 30_000) // closed history the restart must see
	rtExercise(t, a)
	require.NotEmpty(t, a.rt.cpTurns, "the fixture series must declare so cpTurns round-trips something")
	require.Equal(t, core.Tokens(34_000), a.rt.contextTokens, "30 000 closed + 4 000 open after the Evaluate")
	require.Equal(t, 1, a.rt.burnSamples)
	require.InDelta(t, 4_000.0, a.rt.burnEWMA, 1e-9)
	require.NoError(t, a.rt.Persist(context.Background()))
	require.False(t, a.rt.dirty)
	require.FileExists(t, a.statePath(stateFileBOCD))
	require.FileExists(t, a.statePath(stateFileScheduler))

	b := newRTFixture(t, withRoot(a))
	b.bind(rtSession)
	require.Equal(t, a.rt.contextTokens, b.rt.contextTokens, "the restore recounts closed + open, not open alone")
	require.Equal(t, a.rt.lastAPICallTS, b.rt.lastActivity, "the restored API-call anchor is the last activity")

	// The restore is never a burn sample: the first activity re-baselines, the first Evaluate's
	// recount changes nothing, and only tokens observed AFTER the restart produce a sample.
	t1 := b.rt.lastAPICallTS
	b.rt.NotifyActivity(t1 + 60_000)
	require.Equal(t, 1, b.rt.burnSamples, "the first post-restore activity is a baseline, not a sample")
	require.InDelta(t, 4_000.0, b.rt.burnEWMA, 1e-9)
	require.Equal(t, float64(a.rt.contextTokens), b.evaluate(t).Breakdown["context_tokens"])
	b.rt.NotifyActivity(t1 + 120_000)
	require.Equal(t, 1, b.rt.burnSamples, "the recount is not a jump, so no sample")
	require.InDelta(t, 4_000.0, b.rt.burnEWMA, 1e-9)
	b.rt.AddOpenSegmentTokens(6_000)
	b.rt.NotifyActivity(t1 + 180_000)
	require.Equal(t, 2, b.rt.burnSamples)
	require.InDelta(t, burnEWMAAlpha*3_000+(1-burnEWMAAlpha)*4_000, b.rt.burnEWMA, 1e-9,
		"6 000 tokens over the two minutes since the baseline, folded onto the restored EWMA")

	// b has moved on; a second, untouched restore is what the field-by-field comparison reads.
	b = newRTFixture(t, withRoot(a))
	b.bind(rtSession)
	require.Equal(t, a.rt.det.State(), b.rt.det.State())
	require.Equal(t, a.rt.deltaEWMA, b.rt.deltaEWMA)
	require.Equal(t, a.rt.deltaSamples, b.rt.deltaSamples)
	require.Equal(t, a.rt.burnEWMA, b.rt.burnEWMA)
	require.Equal(t, a.rt.burnSamples, b.rt.burnSamples)
	require.Equal(t, a.rt.cpTurns, b.rt.cpTurns)
	require.Equal(t, a.rt.rounds, b.rt.rounds)
	require.Equal(t, a.rt.frontier, b.rt.frontier)
	require.Equal(t, a.rt.residual, b.rt.residual)
	require.Equal(t, a.rt.lastCheckpointSeq, b.rt.lastCheckpointSeq)
	require.Equal(t, a.rt.lastDecision, b.rt.lastDecision)
	require.Equal(t, a.rt.lastCompactionTS, b.rt.lastCompactionTS)
	require.Equal(t, a.rt.lastAPICallTS, b.rt.lastAPICallTS)
	require.Equal(t, a.rt.lastRequestStartTS, b.rt.lastRequestStartTS)
	require.Equal(t, a.rt.sessionStartTS, b.rt.sessionStartTS)
	require.Equal(t, a.rt.maxTurn, b.rt.maxTurn)
	require.Equal(t, a.rt.openSegTokens, b.rt.openSegTokens)
	require.Zero(t, b.log.count(logWarn))
	require.Zero(t, b.log.count(logLoud))
}

// TestRuntime_PersistRoundTrip_FutureStampRepaired is ruling R59: a parsable document whose
// stamps are later than the clock, or whose counters are out of range, must not wedge the
// restarted runtime — the stamps clamp to now, the counters to their floors, the EWMAs to
// unmeasured — and one Warn says so.
func TestRuntime_PersistRoundTrip_FutureStampRepaired(t *testing.T) {
	t.Parallel()
	a := newRTFixture(t)
	a.bind(rtSession)
	rtExercise(t, a)
	require.NoError(t, a.rt.Persist(context.Background()))

	// Hand-edit the document: every stamp a day ahead of the clock, negative counters, a burn
	// EWMA that is negative and a δ EWMA that claims samples it has no value for.
	raw, err := os.ReadFile(a.statePath(stateFileScheduler))
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(raw, &m))
	future := int64(a.now()) + int64(24*time.Hour/time.Millisecond)
	for _, k := range []string{"session_start_ts", "last_compaction_ts", "last_api_call_ts", "last_cache_write_ts", "last_request_start_ts"} {
		require.Contains(t, m, k)
		m[k] = future
	}
	m["open_segment_tokens"] = -5
	m["residual_tokens"] = -7
	m["burn_ewma_tokens_per_min"] = -1.0
	m["delta_ewma_seconds"] = 0.0
	m["delta_samples"] = 3
	out, err := json.Marshal(m)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(a.statePath(stateFileScheduler), out, 0o600))

	b := newRTFixture(t, withRoot(a))
	b.bind(rtSession)
	now := b.now()
	require.Equal(t, now, b.rt.lastAPICallTS, "a future stamp clamps to the clock")
	require.Equal(t, now, b.rt.lastActivity)
	require.Equal(t, now, b.rt.lastCompactionTS)
	require.Equal(t, now, b.rt.lastCacheWriteTS)
	require.Equal(t, now, b.rt.lastRequestStartTS)
	require.Equal(t, now, b.rt.sessionStartTS)
	require.Zero(t, b.rt.openSegTokens)
	require.Zero(t, b.rt.residual)
	require.Zero(t, b.rt.burnEWMA)
	require.Zero(t, b.rt.burnSamples)
	require.Zero(t, b.rt.deltaEWMA)
	require.Zero(t, b.rt.deltaSamples)
	require.Nil(t, b.rt.deltaPtr(), "an inconsistent δ pair restores as unmeasured")
	require.Equal(t, []string{msgStateRepaired}, b.log.msgs(logWarn), "exactly one Warn names the repair")
	require.Zero(t, b.log.count(logLoud))
	require.Equal(t, a.rt.cpTurns, b.rt.cpTurns, "the sane fields still round-trip")
	require.Equal(t, a.rt.det.State(), b.rt.det.State())

	// The restarted runtime is live: real activity is accepted, idleness is reported once the
	// detect-after window has elapsed since it, and the TTL gap is measured from the clamped
	// anchor rather than pinned at 0.
	b.rt.NotifyActivity(now + 1_000)
	require.Equal(t, now+1_000, b.rt.lastActivity, "NotifyActivity with a real now is accepted")
	after := time.Duration(b.cfg.Scheduler.Idle.DetectAfterSeconds) * time.Second
	b.clock.Advance(after + 2*time.Second)
	since, idle := b.rt.IdleSince()
	require.Equal(t, now+1_000, since)
	require.True(t, idle, "IdleSince fires after the restart")
	d := b.evaluate(t)
	require.Positive(t, d.Breakdown["idle_gap_seconds"])
}

func TestRuntime_StateDiscardedOnSessionMismatch(t *testing.T) {
	t.Parallel()
	a := newRTFixture(t)
	a.bind("s1")
	rtExercise(t, a)
	require.NoError(t, a.rt.Persist(context.Background()))

	b := newRTFixture(t, withRoot(a))
	b.bind("s2")
	fresh := scheduler.NewBOCD(b.cfg.Scheduler.Changepoint.HazardRate, b.cfg.Scheduler.Changepoint.Features)
	require.Equal(t, fresh.State(), b.rt.det.State(), "a fresh detector, not s1's posterior")
	require.Empty(t, b.rt.cpTurns)
	require.Zero(t, b.rt.deltaEWMA)
	require.Zero(t, b.rt.deltaSamples)
	require.Zero(t, b.rt.burnEWMA)
	require.Zero(t, b.rt.frontier)
	require.Equal(t, map[core.TurnIndex]struct{}{0: {}}, b.rt.rounds)
	require.Contains(t, b.log.msgs(logInfo), msgStateOtherSession)
	require.Zero(t, b.log.count(logWarn))
	require.Zero(t, b.log.count(logLoud))
}

func TestRuntime_StateDiscardedOnHazardChange(t *testing.T) {
	t.Parallel()
	a := newRTFixture(t)
	a.bind(rtSession)
	rtExercise(t, a)
	require.NoError(t, a.rt.Persist(context.Background()))

	b := newRTFixture(t, withRoot(a), func(fx *rtFixture) { fx.cfg.Scheduler.Changepoint.HazardRate = 0.01 })
	b.bind(rtSession)
	fresh := scheduler.NewBOCD(0.01, b.cfg.Scheduler.Changepoint.Features)
	require.Equal(t, fresh.State(), b.rt.det.State())
	require.NotEqual(t, a.rt.det.State(), b.rt.det.State())
	require.Contains(t, b.log.msgs(logWarn), msgStateModelShapeChanged)
	require.Zero(t, b.log.count(logLoud))
	require.Equal(t, a.rt.deltaEWMA, b.rt.deltaEWMA, "only the detector is discarded; the measurements survive")
}

func TestRuntime_StateDiscardedOnFeatureListChange(t *testing.T) {
	t.Parallel()
	a := newRTFixture(t)
	a.bind(rtSession)
	rtExercise(t, a)
	require.NoError(t, a.rt.Persist(context.Background()))

	b := newRTFixture(t, withRoot(a), func(fx *rtFixture) { fx.cfg.Scheduler.Changepoint.Features = []string{"paths", "tools"} })
	b.bind(rtSession)
	fresh := scheduler.NewBOCD(b.cfg.Scheduler.Changepoint.HazardRate, []string{"paths", "tools"})
	require.Equal(t, fresh.State(), b.rt.det.State())
	require.Contains(t, b.log.msgs(logWarn), msgStateModelShapeChanged)
	require.Zero(t, b.log.count(logLoud))
}

func TestRuntime_CorruptStateFilesSelfHeal(t *testing.T) {
	t.Parallel()
	fx := newRTFixture(t)
	require.NoError(t, os.MkdirAll(filepath.Dir(fx.statePath(stateFileBOCD)), 0o700))
	require.NoError(t, os.WriteFile(fx.statePath(stateFileBOCD), []byte("{not json"), 0o600))
	require.NoError(t, os.WriteFile(fx.statePath(stateFileScheduler), []byte("\x00\x01garbage"), 0o600))

	fx.bind(rtSession)
	fresh := scheduler.NewBOCD(fx.cfg.Scheduler.Changepoint.HazardRate, fx.cfg.Scheduler.Changepoint.Features)
	require.Equal(t, fresh.State(), fx.rt.det.State())
	require.Equal(t, 2, fx.log.count(logLoud), "one Loud per unreadable file: %v", fx.log.msgs(logLoud))
	for _, m := range fx.log.msgs(logLoud) {
		require.Equal(t, msgStateUnreadable, m)
	}
	require.FileExists(t, fx.statePath(stateFileBOCD), "nothing is deleted")

	// A valid bocd.json whose base64 payload fails the detector's CRC is the third corruption.
	// The garbage scheduler.json is removed first so it does not add a Loud per bind below.
	require.NoError(t, os.Remove(fx.statePath(stateFileScheduler)))
	doc := bocdStateDoc{
		Version: stateVersion, Session: rtSession, HazardRate: fx.cfg.Scheduler.Changepoint.HazardRate,
		Features: fx.cfg.Scheduler.Changepoint.Features, State: "AAAA",
	}
	b, err := json.Marshal(doc)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(fx.statePath(stateFileBOCD), b, 0o600))
	fx.bind("other")
	fx.bind(rtSession)
	require.Equal(t, 3, fx.log.count(logLoud))
	require.Equal(t, fresh.State(), fx.rt.det.State())
}

// ── Bounded history ─────────────────────────────────────────────────────────────────────────

func TestRuntime_ChangepointTurnsCapped(t *testing.T) {
	t.Parallel()
	const n = 10_000
	fx := newRTFixture(t)
	fx.bind(rtSession)
	fx.rt.mu.Lock()
	for i := 1; i <= n; i++ {
		fx.rt.recordChangepointLocked(core.TurnIndex(i))
	}
	fx.rt.recordChangepointLocked(core.TurnIndex(n)) // duplicate of the newest
	fx.rt.mu.Unlock()
	require.Len(t, fx.rt.cpTurns, maxTurnHistory)
	require.Equal(t, core.TurnIndex(n-maxTurnHistory+1), fx.rt.cpTurns[0], "oldest evicted first")
	require.Equal(t, core.TurnIndex(n), fx.rt.cpTurns[maxTurnHistory-1])
	require.True(t, slices.IsSortedFunc(fx.rt.cpTurns, func(a, b core.TurnIndex) int { return int(a - b) }))

	for i := 1; i <= n; i++ {
		fx.rt.NoteAPIRound(core.TurnIndex(i))
	}
	require.Len(t, fx.rt.rounds, maxTurnHistory)
	_, hasNewest := fx.rt.rounds[n]
	require.True(t, hasNewest)
	_, hasOldest := fx.rt.rounds[0]
	require.False(t, hasOldest, "turn 0 was the oldest round boundary and is evicted first")
	_, hasEvicted := fx.rt.rounds[n-maxTurnHistory]
	require.False(t, hasEvicted)
	_, hasKept := fx.rt.rounds[n-maxTurnHistory+1]
	require.True(t, hasKept)
}

func TestRuntime_NoteAPIRoundRecordsBoundary(t *testing.T) {
	t.Parallel()
	fx := newRTFixture(t)
	fx.bind(rtSession)
	_, ok := fx.rt.rounds[0]
	require.True(t, ok, "turn 0 is a round boundary from bind")

	// A changepoint at turn 0 and at turn 7, both anchored in the graph.
	require.NoError(t, fx.graph.AddNode(dag.Node{ID: "prompt:0", Kind: dag.KindUserPrompt, Turn: 0, Pos: 0}))
	require.NoError(t, fx.graph.AddNode(dag.Node{ID: "prompt:7", Kind: dag.KindUserPrompt, Turn: 7, Pos: 7_000}))
	fx.store.segs.addSegment(t, rtSession, 0, 9, 9_000)
	fx.rt.mu.Lock()
	fx.rt.recordChangepointLocked(0)
	fx.rt.recordChangepointLocked(7)
	fx.rt.maxTurn = 9
	fx.rt.mu.Unlock()

	d := fx.evaluate(t)
	require.Equal(t, 2.0, d.Breakdown["candidates_supplied"])
	require.Equal(t, 1.0, d.Breakdown["candidates"], "only turn 0 is a round boundary so far")

	fx.rt.NoteAPIRound(7)
	_, ok = fx.rt.rounds[7]
	require.True(t, ok)
	d = fx.evaluate(t)
	require.Equal(t, 2.0, d.Breakdown["candidates"], "turn 7 is now a round boundary")
	require.NotContains(t, d.Breakdown, "round_boundary_relaxed")

	cands, err := fx.rt.asm.Assemble(context.Background(), rtSession, fx.rt.cpTurns, fx.rt.rounds, nil, fx.rt.maxTurn)
	require.NoError(t, err)
	require.Len(t, cands, 2)
	for _, c := range cands {
		require.True(t, c.RoundBoundary, "turn %d", c.Turn)
	}
}

// ── Close ───────────────────────────────────────────────────────────────────────────────────

func TestRuntime_CloseIsIdempotent(t *testing.T) {
	t.Parallel()
	fx := newRTFixture(t, func(fx *rtFixture) { fx.writer = newFakeWriter(fx.store.segs) })
	fx.bind(rtSession)
	draft, err := fx.writer.Begin(context.Background(), rtSession, 0, checkpoint.SourceSet{})
	require.NoError(t, err)

	require.NoError(t, CloseSchedulerRuntime(fx.rt))
	require.Equal(t, int64(1), fx.counter(counterPersist))
	require.Zero(t, fx.writer.abortCalls)
	require.Contains(t, fx.writer.drafts, draft, "scheduler close preserves the owner's draft")

	require.NoError(t, CloseSchedulerRuntime(fx.rt))
	require.Equal(t, int64(2), fx.counter(counterPersist), "Persist runs once per call")
	require.Zero(t, fx.writer.abortCalls, "scheduler close never aborts the owner's draft")
	require.FileExists(t, fx.statePath(stateFileScheduler))
}

func TestSchedulerSnapshotOf_ReportsRuntimeState(t *testing.T) {
	t.Parallel()
	fx := newRTFixture(t)
	fx.bind(rtSession)
	rtExercise(t, fx)

	snap, ok := SchedulerSnapshotOf(fx.rt)
	require.True(t, ok)
	require.Equal(t, fx.rt.lastDecision, snap.LastDecision)
	require.Equal(t, core.TurnIndex(33), snap.FrontierTurn)
	require.Equal(t, core.Tokens(9_120), snap.ResidualTokens)
	require.Equal(t, core.Tokens(fx.cfg.Checkpoint.Frontier.MaxResidualTokens), snap.MaxResidualTokens)
	require.Equal(t, 20.0, snap.DeltaSeconds)
	require.Equal(t, 1, snap.DeltaSamples)
	require.Equal(t, fx.rt.burnEWMA, snap.BurnRateTokensPerMin)
	require.Equal(t, fx.rt.cpTurns, snap.ChangepointTurns)
	require.Equal(t, int64(fx.rt.lastActivity), snap.IdleSinceMS)
	require.Equal(t, "unknown", snap.RegimeSource)
	require.Equal(t, windowSourceHostDefault, snap.WindowSource)

	_, ok = SchedulerSnapshotOf(nil)
	require.False(t, ok)
	_, ok = SchedulerSnapshotOf(fakeForeignRuntime{})
	require.False(t, ok)
	_, ok = PrecomputedSlice(fx.rt)
	require.False(t, ok, "nothing precomputed yet")
	_, ok = PrecomputedSlice(nil)
	require.False(t, ok)
}

// ── Concurrency ─────────────────────────────────────────────────────────────────────────────

func TestRuntime_ConcurrentObserveEvaluatePersist(t *testing.T) {
	t.Parallel()
	const (
		goroutines = 8
		opsEach    = 700 // 8 × 700 = 5 600 operations
		persists   = 30  // per persisting goroutine; every WriteAtomic fsyncs
	)
	fx := newRTFixture(t)
	fx.bind(rtSession)
	fx.store.segs.addSegment(t, rtSession, 1, 50, 120_000)
	series := rtStepSeries(200, rtStepShift)
	ctx := context.Background()

	var wg sync.WaitGroup
	errs := make(chan error, goroutines*persists)
	for g := range goroutines {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			switch g % 4 {
			case 0:
				for i := range opsEach {
					fx.rt.Observe(ctx, series[i%len(series)], core.TurnIndex(i%50+1))
				}
			case 1:
				for range opsEach {
					if _, err := fx.rt.Evaluate(ctx); err != nil {
						errs <- err
						return
					}
				}
			case 2:
				for i := range opsEach {
					fx.rt.NotifyActivity(fx.now() + core.UnixMilli(i))
					fx.rt.AddOpenSegmentTokens(core.Tokens(i%10 + 1))
					fx.rt.NoteAPIRound(core.TurnIndex(i % 50))
					fx.rt.IdleSince()
					fx.rt.PrecomputedSlice()
				}
			case 3:
				for range persists {
					if err := fx.rt.Persist(ctx); err != nil {
						errs <- err
						return
					}
				}
			}
		}(g)
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Minute):
		t.Fatal("deadlock: the runtime did not finish 5 000 concurrent operations")
	}
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	require.NoError(t, fx.rt.Persist(ctx))
	require.FileExists(t, fx.statePath(stateFileScheduler))
}

func TestRuntime_OpenSegmentTokensResetOnClose(t *testing.T) {
	t.Parallel()
	fx := newRTFixture(t)
	fx.bind(rtSession)
	ctx := context.Background()
	_, err := fx.store.segs.Open(ctx, store.Segment{Session: rtSession, StartTurn: 0})
	require.NoError(t, err)

	fx.rt.AddOpenSegmentTokens(4_000)
	fx.rt.mu.Lock()
	err = fx.rt.closeSegmentLocked(ctx, 5, scheduler.Features{PathJaccard: 1}, "test")
	fx.rt.mu.Unlock()
	require.NoError(t, err)

	closes := fx.store.segs.closeCalls
	require.Len(t, closes, 1, "closeSegmentLocked is C2's (scheduler_frontier.go); red against C1's stub by design")
	require.Equal(t, 4_000.0, closes[0].Feats[segTokensFeature], "the close carries the open accumulator as the tokens pseudo-feature")
	require.Equal(t, core.Tokens(0), fx.rt.openSegTokens, "the successor starts empty")

	fx.rt.AddOpenSegmentTokens(1_500)
	fx.rt.NoteAPIRound(5)
	d := fx.evaluate(t)
	require.Equal(t, 5_500.0, d.Breakdown["context_tokens"], "4 000 now closed + 1 500 open, no double count")
}

func TestRuntime_ObserveClosesSegmentOnChangepoint(t *testing.T) {
	t.Parallel()
	fx := newRTFixture(t)
	fx.bind(rtSession)
	_, err := fx.store.segs.Open(context.Background(), store.Segment{Session: rtSession, StartTurn: 0})
	require.NoError(t, err)

	declared := declaredTurns(fx.feed(rtStepSeries(200, rtStepShift)))
	require.Len(t, declared, 1)
	at := declared[0]

	closes := fx.store.segs.closeCalls
	require.Len(t, closes, 1, "closeSegmentLocked is C2's (scheduler_frontier.go); red against C1's stub by design")
	require.Equal(t, core.SegmentID(1), closes[0].ID)
	require.Equal(t, at, closes[0].EndTurn)
	opens := fx.store.segs.openCalls
	require.Len(t, opens, 2, "the fixture's open, then the roll-open")
	require.Equal(t, at+1, opens[1].StartTurn)
	require.Equal(t, rtSession, opens[1].Session)
}

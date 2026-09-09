// V5 §4.13 — the retained identifier TestV5_SkiRentalChangesTheChosenCutButNeverBlocksAnUrgentOne,
// under the CURRENT criterion: "Retire ski-rental native cut assertion; SP-12 owns deprecated
// compatibility and local cadence."
//
// The historical row asserted a ski-rental clause INSIDE scheduler.Evaluate: a "ski_rental_defer"
// reason and a Breakdown["ski_rental_threshold"] term that changed which cut was chosen. No such
// clause exists on this tree and the guarantee is retired: SkiRentalShouldWrite has no production
// caller (plans/sdd/V4-SP-19-migration-reconciliation/M0-01-inventory.md §4.2), Inputs.
// ExpectedRemainingReads is carried on the struct and read by nothing, and the write policy is
// registered as an EXPERIMENTAL, default-off capability (internal/scheduler/unsupported.go). This
// row therefore asserts, with every producer real, that
//
//   - the pure composite trigger and the daemon-owned runtime carry NO ski-rental term: the
//     Decision is byte-identical whatever ExpectedRemainingReads says, and an urgent
//     (hard-ceiling) cut fires with UrgencyNow whether the write policy would have "deferred" or
//     not — the "never blocks an urgent one" half of the identifier, kept;
//   - the capability register records the policy as disabled by default, never as passed, and
//     records a native cut as unsupported under every switch;
//   - the deprecated compatibility surface SP-12 owns still computes the break-even from the
//     RESOLVED cache regime, never from a literal: 12.5 expected reads at the five-minute floor,
//     20 at the one-hour TTL, doubling with the configured write multiplier.
//
// Negative controls: the register's process-wide opt-in (scheduler.EnableExperimentalPolicies) is
// a real switch, and flipping it flips the register's answer — so "disabled" is a live reading,
// not a constant; and the runtime's Decision stays free of ski-rental terms even under that
// opt-in, so the opt-in registers a policy without wiring one into the cut path. The retirement
// assertion itself (no clause in Evaluate) was proven non-vacuous during authoring by a one-line
// source edit that reintroduced the clause and turned this test red; see
// plans/sdd/V5-VERIFY/x13-disposition.md.
package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/daemon"
	"github.com/qompack/qompack/internal/dag"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/obs"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/scheduler"
	"github.com/qompack/qompack/internal/store"
	"github.com/qompack/qompack/internal/testutil"
)

// x13Session is this row's session identity.
const x13Session = core.SessionID("sess-integration-v5-x13")

// x13SkiRentalMarker is the substring every historical ski-rental term carried
// ("ski_rental_defer", "ski_rental_threshold"); a Decision is clean when no reason and no
// Breakdown key contains it.
const x13SkiRentalMarker = "ski_rental"

// x13HistoricalExpectedReads is the historical §4.13 input: five expected remaining reads, below
// the ski-rental break-even under every priced regime, so the retired clause would have deferred.
const x13HistoricalExpectedReads = 5

// x13ExpectedReadsSweep is the sweep the retirement is asserted over: the zero value, the
// historical deferring input, and a value far above the one-hour break-even (20). A live clause
// would have to produce different Decisions for at least two of them.
var x13ExpectedReadsSweep = []float64{0, x13HistoricalExpectedReads, 100}

// The documented host cache variables the regime ladder reads (internal/scheduler/cacheregime.go).
const (
	x13EnvForce5M  = "FORCE_PROMPT_CACHING_5M"
	x13EnvEnable1H = "ENABLE_PROMPT_CACHING_1H"
	x13EnvDisable  = "DISABLE_PROMPT_CACHING"
)

// The BOCD step series that makes the real detector declare a changepoint, transcribed from
// internal/daemon/scheduler_runtime_test.go (rtStepSeries), itself a transcription of
// internal/scheduler/bocd_test.go, which neither this package nor daemon can import.
const (
	x13SeriesSeed          uint32 = 0x5EED_1234
	x13SeriesNoise                = 0.05
	x13SeriesBaseMean             = 0.2
	x13GapReferenceSeconds        = 600.0
	x13StepShift                  = 0.6
	x13SeriesLen                  = 200
)

// x13PosStride is the token position stride between consecutive turns in the DAG fixture; any
// positive value works, it only has to make every turn resolve to a distinct position.
const x13PosStride = 750

// x13Epsilon nudges an expected-reads value just across a computed break-even; the comparison in
// SkiRentalShouldWrite is strict, so the threshold itself must read false and threshold+ε true.
const x13Epsilon = 1e-9

// x13LCG is bocd_test.go's 32-bit LCG (Numerical Recipes constants): bit-identical everywhere.
type x13LCG struct{ state uint32 }

func (g *x13LCG) next() float64 {
	g.state = g.state*1664525 + 1013904223
	return float64(g.state>>8) / float64(1<<24)
}

// x13FeaturesAt builds a Features whose every mapped stream equals v under the detector's
// orientation (PathJaccard and LexicalCohesion inverted, GapSeconds un-compressed).
func x13FeaturesAt(v float64) scheduler.Features {
	return scheduler.Features{
		PathJaccard:     1 - v,
		ToolShift:       v,
		LexicalCohesion: 1 - v,
		GapSeconds:      math.Expm1(v * math.Log1p(x13GapReferenceSeconds)),
		TodoTransition:  v,
	}
}

// x13StepSeries is n observations at mean 0.2, then mean 0.2+shift after n/2, with ±0.05 uniform
// noise from the fixed seed.
func x13StepSeries(n int, shift float64) []scheduler.Features {
	g := &x13LCG{state: x13SeriesSeed}
	out := make([]scheduler.Features, n)
	for i := range out {
		mean := x13SeriesBaseMean
		if i >= n/2 {
			mean += shift
		}
		v := min(max(mean+(g.next()-0.5)*2*x13SeriesNoise, 0), 1)
		out[i] = x13FeaturesAt(v)
	}
	return out
}

// x13SkiRentalTerms lists every reason and Breakdown key of d that carries the historical
// ski-rental marker. Empty is the retired state.
func x13SkiRentalTerms(d scheduler.Decision) []string {
	var terms []string
	for _, r := range d.Reasons {
		if strings.Contains(string(r), x13SkiRentalMarker) {
			terms = append(terms, "reason:"+string(r))
		}
	}
	for k := range d.Breakdown {
		if strings.Contains(k, x13SkiRentalMarker) {
			terms = append(terms, "breakdown:"+k)
		}
	}
	slices.Sort(terms)
	return terms
}

// x13Getenv is a scripted host environment for ResolveCacheRegime: exactly the given variables,
// nothing from the process.
func x13Getenv(env map[string]string) func(string) string {
	return func(k string) string { return env[k] }
}

// x13Regime resolves the real regime ladder for a main-agent session under env.
func x13Regime(cfg config.Config, env map[string]string) scheduler.CacheRegime {
	return scheduler.ResolveCacheRegime(x13Getenv(env), cfg.Scheduler, "", false,
		cfg.Runtime.Scheduler.Cache.AssumeMaxTTLSeconds)
}

// x13Inputs is the historical row's scripted base: the host's §2.5 worked-example window, a warm
// cache one second old, one legal round-boundary candidate, and the deferring expected-reads
// input. Scenario functions adjust ContextTokens and the TTL anchor.
func x13Inputs(cfg config.Config, reg scheduler.CacheRegime, now core.UnixMilli) scheduler.Inputs {
	window := scheduler.EffectiveWindow(scheduler.HostDefaultContextWindow, scheduler.HostDefaultMaxOutput)
	soft := scheduler.SoftFloor(window, cfg.Scheduler)
	const oneSecondMS = 1_000
	return scheduler.Inputs{
		Now:                     now,
		EffectiveWindow:         window,
		MaxOutputTokens:         scheduler.HostDefaultMaxOutput,
		LastAPICallTS:           now - oneSecondMS,
		LastRequestStartTS:      now - oneSecondMS,
		LastCompactionTS:        now - oneSecondMS,
		ExpectedRemainingReads:  x13HistoricalExpectedReads,
		Cfg:                     cfg.Scheduler,
		CouplingLambda:          cfg.Selection.Submodular.Lambda,
		Regime:                  reg,
		ExpiringTriggerFraction: cfg.Runtime.Scheduler.Cache.ExpiringTriggerFraction,
		AssumeMaxTTLSeconds:     cfg.Runtime.Scheduler.Cache.AssumeMaxTTLSeconds,
		Candidates: []scheduler.Candidate{{
			Pos: int(soft / 2), Turn: 10, SegmentID: 1, RoundBoundary: true,
			ReclaimableTokens: soft / 4,
		}},
	}
}

// x13Taps is the slice of the daemon's concrete runtime the scheduler tap drives
// (internal/daemon/scheduler_tap.go); scheduler.Runtime itself does not expose it.
type x13Taps interface {
	BindSession(id core.SessionID, e *hookio.Event)
	AddOpenSegmentTokens(tok core.Tokens)
	NoteAPIRound(at core.TurnIndex)
}

// x13OpenRuntime composes the daemon-owned scheduler runtime over the real store and the real DAG
// of one disposable project, exactly as the composition root does, with the project's scripted
// host environment as its getenv.
func x13OpenRuntime(t *testing.T, p *testutil.Project) (scheduler.Runtime, x13Taps, store.Store, dag.Graph) {
	t.Helper()
	s := p.Store(t)
	g, err := dag.Open(p.Root, p.Cfg, p.Log)
	require.NoError(t, err)
	rt, err := daemon.NewSchedulerRuntime(daemon.SchedulerRuntimeOptions{
		ProjectRoot: p.Root,
		Cfg:         p.Cfg,
		Clock:       p.Clock,
		Log:         p.Log,
		Metrics:     obs.New(p.Clock),
		Store:       s,
		Graph:       g,
		Getenv:      p.Getenv,
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, daemon.CloseSchedulerRuntime(rt)) })
	t.Cleanup(scheduler.DisablePSelection)
	taps, ok := rt.(x13Taps)
	require.True(t, ok, "the daemon's runtime must expose the tap surface the composition root drives")
	return rt, taps, s, g
}

// x13PopulateGraph gives every turn of the series one ephemeral tool-result node at a distinct
// position, so the candidate assembler can resolve any changepoint turn to a position and the
// reclaimable index has something after it.
func x13PopulateGraph(t *testing.T, g dag.Graph, turns int, now core.UnixMilli) {
	t.Helper()
	for turn := 1; turn <= turns; turn++ {
		id := core.ToolUseID(fmt.Sprintf("toolu_v5x13_%03d", turn))
		pos := turn * x13PosStride
		require.NoError(t, g.AddNode(dag.Node{
			ID: dag.ToolUseNode(id), Kind: dag.KindToolUse, Turn: core.TurnIndex(turn), TS: now, Pos: pos, Ref: "Bash",
		}))
		require.NoError(t, g.AddNode(dag.Node{
			ID: dag.ToolResultNode(id), Kind: dag.KindToolResult, Turn: core.TurnIndex(turn), TS: now, Pos: pos,
			Ref: string(id), Tokens: x13PosStride, Ephemeral: true,
		}))
	}
}

// TestV5_SkiRentalChangesTheChosenCutButNeverBlocksAnUrgentOne is V5-VERIFY §4.13 under its
// current criterion. The identifier is retained; the "changes the chosen cut" half is retired and
// asserted in the negative (nothing changes), the "never blocks an urgent one" half is kept.
func TestV5_SkiRentalChangesTheChosenCutButNeverBlocksAnUrgentOne(t *testing.T) {
	t.Run("evaluate_has_no_ski_rental_clause_and_expected_reads_are_inert", func(t *testing.T) {
		p := testutil.NewProject(t)
		now := core.NowMilli(p.Clock)
		reg := x13Regime(p.Cfg, map[string]string{x13EnvForce5M: "1"})
		require.Equal(t, "force_5m", reg.Source, "the scripted environment must resolve to the known five-minute regime")

		// The historical row's premise holds on the compatibility surface: at five expected
		// reads the write policy would defer under this regime …
		require.False(t, scheduler.SkiRentalShouldWrite(x13HistoricalExpectedReads, reg.ReadMultiplier, reg.WriteMultiplier),
			"five expected reads is below the five-minute break-even; the historical clause would have deferred")

		window := scheduler.EffectiveWindow(scheduler.HostDefaultContextWindow, scheduler.HostDefaultMaxOutput)
		soft := scheduler.SoftFloor(window, p.Cfg.Scheduler)
		hard := scheduler.HardCeiling(window, p.Cfg.Scheduler)
		require.Less(t, soft, hard)

		type scenario struct {
			name   string
			adjust func(in *scheduler.Inputs)
			check  func(t *testing.T, d scheduler.Decision)
		}
		scenarios := []scenario{
			{
				// (a) soft reasons only, warm TTL: nothing fires — and nothing DEFERS either. The
				// only reason is the AND-gate itself; there is no ski-rental disjunct to report.
				name:   "soft_floor_only_warm",
				adjust: func(in *scheduler.Inputs) { in.ContextTokens = soft + 1 },
				check: func(t *testing.T, d scheduler.Decision) {
					require.False(t, d.ShouldCompact)
					require.Equal(t, []scheduler.TriggerReason{scheduler.TriggerSoftFloor}, d.Reasons,
						"above the floor with every disjunct false, soft_floor is the only reason; a deferral reason would be a live clause")
					require.Equal(t, scheduler.UrgencyNone, d.Urgency)
					require.Equal(t, scheduler.TTLWarm, d.TTL)
				},
			},
			{
				// (b) hard ceiling present: the urgent cut fires NOW, with the write policy still
				// saying "defer" on the compatibility surface.
				name:   "hard_ceiling_is_urgent",
				adjust: func(in *scheduler.Inputs) { in.ContextTokens = hard + 1 },
				check: func(t *testing.T, d scheduler.Decision) {
					require.True(t, d.ShouldCompact, "an urgent cut must never be blocked")
					require.Equal(t, scheduler.UrgencyNow, d.Urgency)
					require.Contains(t, d.Reasons, scheduler.TriggerHardCeiling)
					require.Equal(t, 10, int(d.P.Turn), "the one legal candidate is the chosen cut")
				},
			},
			{
				// (c) cold TTL: the idle-cold cut fires as an advisory, again with no deferral.
				name: "cold_cache_fires",
				adjust: func(in *scheduler.Inputs) {
					in.ContextTokens = soft + 1
					const msPerSecond = 1_000
					gone := core.UnixMilli((reg.TTLMaxSeconds + 1) * msPerSecond)
					in.LastAPICallTS, in.LastRequestStartTS = now-gone, now-gone
				},
				check: func(t *testing.T, d scheduler.Decision) {
					require.True(t, d.ShouldCompact)
					require.Equal(t, scheduler.TTLCold, d.TTL)
					require.Contains(t, d.Reasons, scheduler.TriggerIdleColdCache)
					require.Equal(t, scheduler.UrgencyAdvisory, d.Urgency)
				},
			},
		}

		for _, sc := range scenarios {
			t.Run(sc.name, func(t *testing.T) {
				base := x13Inputs(p.Cfg, reg, now)
				sc.adjust(&base)
				first := scheduler.Evaluate(base)
				sc.check(t, first)
				require.Empty(t, x13SkiRentalTerms(first),
					"the retired clause must leave no reason and no Breakdown term behind")
				require.NotContains(t, first.Breakdown, "ski_rental_threshold",
					"the historical Breakdown key is retired, not renamed")

				// The retirement: ExpectedRemainingReads is inert. Every value in the sweep
				// yields the byte-identical Decision — a clause that "changed the chosen cut"
				// would have to disagree somewhere in this sweep.
				for _, reads := range x13ExpectedReadsSweep {
					in := base
					in.ExpectedRemainingReads = reads
					got := scheduler.Evaluate(in)
					require.Equal(t, first, got,
						"ExpectedRemainingReads=%v changed the Decision: a ski-rental clause is live in Evaluate", reads)
				}
			})
		}
	})

	t.Run("real_runtime_urgent_cut_fires_with_no_ski_rental_term", func(t *testing.T) {
		p := testutil.NewProject(t, testutil.WithEnv(x13EnvForce5M, "1"))
		ctx := context.Background()
		rt, taps, s, g := x13OpenRuntime(t, p)
		now := core.NowMilli(p.Clock)

		// The observer's segment for this session, and a DAG the assembler can resolve turns in.
		_, err := s.Segments().Open(ctx, store.Segment{Session: x13Session, StartTurn: 0, StartTS: now})
		require.NoError(t, err)
		x13PopulateGraph(t, g, x13SeriesLen, now)

		taps.BindSession(x13Session, &hookio.Event{
			HookEventName: "SessionStart", SessionID: x13Session, Source: "startup", CWD: p.Root,
		})

		// An empty session: below the floor, no trigger, and no ski-rental term either.
		quiet, err := rt.Evaluate(ctx)
		require.NoError(t, err)
		require.False(t, quiet.ShouldCompact)
		require.Positive(t, quiet.HardCeilingTokens, "the window must have resolved from the host default")
		require.Empty(t, x13SkiRentalTerms(quiet))

		// Push the live context one token past the ceiling the runtime itself reported, then let
		// the REAL detector find the task boundary the step series carries; each declared turn
		// is also a round boundary, so the assembler yields at least one legal cut.
		taps.AddOpenSegmentTokens(quiet.HardCeilingTokens + 1)
		var declared []core.TurnIndex
		for i, f := range x13StepSeries(x13SeriesLen, x13StepShift) {
			turn := core.TurnIndex(i + 1)
			if rt.Observe(ctx, f, turn).AtChangepoint {
				declared = append(declared, turn)
			}
		}
		require.NotEmpty(t, declared, "the step series must make the real BOCD declare a changepoint")
		for _, turn := range declared {
			taps.NoteAPIRound(turn)
		}
		taps.NoteAPIRound(core.TurnIndex(x13SeriesLen + 1))

		urgent, err := rt.Evaluate(ctx)
		require.NoError(t, err)
		require.Equal(t, float64(quiet.HardCeilingTokens+1), urgent.Breakdown["context_tokens"],
			"the closed segment plus the open accumulator is the live context")
		require.True(t, urgent.ShouldCompact, "an urgent cut must never be blocked: hard_ceiling with a legal candidate compacts")
		require.Equal(t, scheduler.UrgencyNow, urgent.Urgency)
		require.Contains(t, urgent.Reasons, scheduler.TriggerSoftFloor)
		require.Contains(t, urgent.Reasons, scheduler.TriggerHardCeiling)
		require.GreaterOrEqual(t, urgent.Breakdown["candidates_supplied"], 1.0)
		require.Contains(t, declared, urgent.P.Turn, "the chosen cut is one of the detector's own boundaries")
		require.True(t, urgent.P.RoundBoundary)
		require.Empty(t, x13SkiRentalTerms(urgent), "the runtime adds host keys after Evaluate; none of them is a ski-rental term")

		// The persisted decision — what /qompack:status and a restart read back — is equally
		// clean, and the snapshot SP-14 consumes agrees with the Decision returned.
		require.NoError(t, rt.Persist(ctx))
		raw, err := os.ReadFile(filepath.Join(paths.Of(p.Root).State, "scheduler.json"))
		require.NoError(t, err)
		require.NotContains(t, string(raw), x13SkiRentalMarker, "state/scheduler.json carries no ski-rental term")
		var doc struct {
			LastDecision struct {
				ShouldCompact bool               `json:"should_compact"`
				Urgency       scheduler.Urgency  `json:"urgency"`
				Breakdown     map[string]float64 `json:"breakdown"`
			} `json:"last_decision"`
		}
		require.NoError(t, json.Unmarshal(raw, &doc))
		require.True(t, doc.LastDecision.ShouldCompact)
		require.Equal(t, scheduler.UrgencyNow, doc.LastDecision.Urgency)
		require.Equal(t, urgent.Breakdown["hard_ceiling"], doc.LastDecision.Breakdown["hard_ceiling"])

		snap, ok := daemon.SchedulerSnapshotOf(rt)
		require.True(t, ok)
		require.Equal(t, urgent, snap.LastDecision)
		require.Equal(t, "force_5m", snap.RegimeSource)

		// NEGATIVE CONTROL, runtime half: the process-wide experimental opt-in is the only switch
		// that can make the register report the write policy available. Under it the runtime's
		// Decision is STILL free of ski-rental terms and the urgent cut still fires: the opt-in
		// registers a policy, it does not wire one into the cut path.
		scheduler.EnableExperimentalPolicies()
		t.Cleanup(scheduler.DisableExperimentalPolicies)
		require.True(t, scheduler.Supports(scheduler.CapSkiRentalWritePolicy, p.Cfg.Scheduler).Available,
			"the opt-in must be live for this control to mean anything")
		optedIn, err := rt.Evaluate(ctx)
		require.NoError(t, err)
		require.True(t, optedIn.ShouldCompact)
		require.Equal(t, scheduler.UrgencyNow, optedIn.Urgency)
		require.Empty(t, x13SkiRentalTerms(optedIn), "opting the register in wires nothing into Evaluate")
		require.Equal(t, urgent.P, optedIn.P, "the chosen cut does not move with the register")

		p.AssertAppendOnly(t)
	})

	t.Run("register_records_the_policy_disabled_and_the_native_cut_unsupported", func(t *testing.T) {
		p := testutil.NewProject(t)
		cfg := p.Cfg.Scheduler
		require.False(t, scheduler.ExperimentalPoliciesEnabled(), "a fresh process is opted out")

		// Disabled is RECORDED — class, availability and a reason — never reported as passed.
		s := scheduler.Supports(scheduler.CapSkiRentalWritePolicy, cfg)
		require.False(t, s.Available)
		require.Equal(t, scheduler.ClassExperimental, s.Class)
		require.NotEmpty(t, s.Reason, "a disabled policy names why it is disabled")
		require.ErrorIs(t, scheduler.Require(scheduler.CapSkiRentalWritePolicy, cfg), scheduler.ErrUnsupported)

		// The native cut the historical row leaned on is unsupported, and no switch changes that.
		native := scheduler.Supports(scheduler.CapNativeCut, cfg)
		require.False(t, native.Available)
		require.Equal(t, scheduler.ClassUnsupported, native.Class)
		require.ErrorIs(t, scheduler.Require(scheduler.CapNativeCut, cfg), scheduler.ErrUnsupported)

		// NEGATIVE CONTROL, register half: the opt-in is a real switch. Flipping it flips the
		// experimental answer — so "disabled" above was a reading, not a constant — and leaves the
		// native cut exactly where it was.
		scheduler.EnableExperimentalPolicies()
		t.Cleanup(scheduler.DisableExperimentalPolicies)
		require.True(t, scheduler.Supports(scheduler.CapSkiRentalWritePolicy, cfg).Available)
		require.NoError(t, scheduler.Require(scheduler.CapSkiRentalWritePolicy, cfg))
		require.ErrorIs(t, scheduler.Require(scheduler.CapNativeCut, cfg), scheduler.ErrUnsupported,
			"no opt-in reaches a native cut")
		scheduler.DisableExperimentalPolicies()
		require.ErrorIs(t, scheduler.Require(scheduler.CapSkiRentalWritePolicy, cfg), scheduler.ErrUnsupported,
			"and the switch is reversible")
	})

	t.Run("compatibility_surface_tracks_the_resolved_regime_not_a_literal", func(t *testing.T) {
		p := testutil.NewProject(t)
		cfg := p.Cfg
		r := cfg.Scheduler.Cache.ReadMultiplier
		require.Positive(t, r)

		// breakEven is w/r under the regime the real ladder resolves; the assertion is that the
		// surface flips exactly there, so the number is derived, never written down.
		breakEven := func(reg scheduler.CacheRegime) float64 { return reg.WriteMultiplier / reg.ReadMultiplier }
		flipsAt := func(t *testing.T, reg scheduler.CacheRegime, want float64) {
			t.Helper()
			require.InDelta(t, want, breakEven(reg), x13Epsilon)
			require.False(t, scheduler.SkiRentalShouldWrite(want, reg.ReadMultiplier, reg.WriteMultiplier),
				"the break-even itself is not strictly greater")
			require.True(t, scheduler.SkiRentalShouldWrite(want+x13Epsilon, reg.ReadMultiplier, reg.WriteMultiplier))
			require.False(t, scheduler.SkiRentalShouldWrite(x13HistoricalExpectedReads, reg.ReadMultiplier, reg.WriteMultiplier),
				"the historical deferring input defers under every priced regime")
		}

		fiveMin := x13Regime(cfg, map[string]string{x13EnvForce5M: "1"})
		require.Equal(t, "force_5m", fiveMin.Source)
		flipsAt(t, fiveMin, cfg.Scheduler.Cache.WriteMultiplier/r)

		oneHour := x13Regime(cfg, map[string]string{x13EnvEnable1H: "1"})
		require.Equal(t, "enable_1h", oneHour.Source)
		flipsAt(t, oneHour, scheduler.HostOneHourWriteMultiplier/r)
		require.Greater(t, breakEven(oneHour), breakEven(fiveMin), "the one-hour write premium raises the break-even")

		// The historical "doubling writeMultiplier doubles it" claim, corrected to its real seam:
		// the config key is the five-minute FLOOR, so it doubles the five-minute break-even and
		// leaves the documented one-hour figure alone.
		doubled := cfg
		doubled.Scheduler.Cache.WriteMultiplier *= 2
		flipsAt(t, x13Regime(doubled, map[string]string{x13EnvForce5M: "1"}), 2*breakEven(fiveMin))
		require.InDelta(t, breakEven(oneHour), breakEven(x13Regime(doubled, map[string]string{x13EnvEnable1H: "1"})), x13Epsilon)

		// A disabled cache has no write premium and no read discount: r = w = 1, break-even 1.
		disabled := x13Regime(cfg, map[string]string{x13EnvDisable: "1"})
		require.True(t, disabled.Disabled)
		require.InDelta(t, 1, breakEven(disabled), x13Epsilon)

		// The guards SP-12's unit rows pin, at the integration seam: no ratio, no write.
		require.False(t, scheduler.SkiRentalShouldWrite(math.Inf(1), 0, fiveMin.WriteMultiplier))
		require.False(t, scheduler.SkiRentalShouldWrite(math.Inf(1), fiveMin.ReadMultiplier, 0))
	})
}

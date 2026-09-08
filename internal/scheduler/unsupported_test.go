package scheduler_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/scheduler"
)

// nowMilli is an arbitrary fixed instant; every assertion here is relative to it.
const nowMilli core.UnixMilli = 1_700_000_000_000

// TestNativeClaimsAreEnforcedUnsupported is the enforcement half of "these are not implemented":
// a caller that asks for a native cut, a veto, an eviction or an O(delta) native compaction gets
// an explicit ErrUnsupported with a reason, under EVERY configuration — the shipped defaults, a
// zero config, and a config with every switch this layer has turned on. No configuration is a way
// in, which is what distinguishes an unsupported claim from a disabled feature.
func TestNativeClaimsAreEnforcedUnsupported(t *testing.T) {
	native := []scheduler.Capability{
		scheduler.CapNativeCut,
		scheduler.CapNativeVeto,
		scheduler.CapNativeEviction,
		scheduler.CapNativeODelta,
	}
	on := config.Defaults().Scheduler
	on.YoungDaly.Enabled = true
	cfgs := map[string]config.SchedulerCfg{
		"defaults":      config.Defaults().Scheduler,
		"zero":          {},
		"everything on": on,
	}

	// Even a process that has opted into the experimental class cannot reach them.
	scheduler.EnableExperimentalPolicies()
	t.Cleanup(scheduler.DisableExperimentalPolicies)

	for name, cfg := range cfgs {
		for _, c := range native {
			s := scheduler.Supports(c, cfg)
			require.False(t, s.Available, "%s/%s must never be available", name, c)
			require.Equal(t, scheduler.ClassUnsupported, s.Class)
			require.NotEmpty(t, s.Reason, "an unsupported answer always carries a reason")

			err := scheduler.Require(c, cfg)
			require.ErrorIs(t, err, scheduler.ErrUnsupported, "%s/%s", name, c)
			require.Contains(t, err.Error(), string(c))
		}
	}
}

// TestExperimentalPoliciesAreOffByDefault proves the DEFAULT is off rather than that the policies
// can be turned off: nothing is configured, nothing is disabled, and the register is asked
// straight out on a fresh process state.
func TestExperimentalPoliciesAreOffByDefault(t *testing.T) {
	require.False(t, scheduler.ExperimentalPoliciesEnabled(),
		"the shipped wiring calls EnableExperimentalPolicies nowhere")

	cfg := config.Defaults().Scheduler
	require.True(t, cfg.YoungDaly.Enabled, "the config key is on; the policy is still off")

	for _, c := range []scheduler.Capability{
		scheduler.CapYoungDalyPacing,
		scheduler.CapSkiRentalWritePolicy,
		scheduler.CapChangepointForecast,
	} {
		s := scheduler.Supports(c, cfg)
		require.False(t, s.Available, "%s is off by default", c)
		require.Equal(t, scheduler.ClassExperimental, s.Class)
		require.ErrorIs(t, scheduler.Require(c, cfg), scheduler.ErrUnsupported)
	}
}

// TestExperimentalOptInIsExplicitAndReversible covers the other direction, so the default-off
// assertion above cannot be satisfied by a register that answers "off" unconditionally.
func TestExperimentalOptInIsExplicitAndReversible(t *testing.T) {
	cfg := config.Defaults().Scheduler
	scheduler.EnableExperimentalPolicies()
	t.Cleanup(scheduler.DisableExperimentalPolicies)
	require.NoError(t, scheduler.Require(scheduler.CapYoungDalyPacing, cfg))

	off := cfg
	off.YoungDaly.Enabled = false
	require.ErrorIs(t, scheduler.Require(scheduler.CapYoungDalyPacing, off), scheduler.ErrUnsupported)

	scheduler.DisableExperimentalPolicies()
	require.ErrorIs(t, scheduler.Require(scheduler.CapYoungDalyPacing, cfg), scheduler.ErrUnsupported)
}

// TestUnknownCapabilityIsRefused keeps the register from being a rubber stamp.
func TestUnknownCapabilityIsRefused(t *testing.T) {
	s := scheduler.Supports("teleport_context", config.Defaults().Scheduler)
	require.False(t, s.Available)
	require.Equal(t, scheduler.ClassUnknown, s.Class)
	require.ErrorIs(t, scheduler.Require("teleport_context", config.Defaults().Scheduler), scheduler.ErrUnsupported)
}

// TestCapabilitiesRegisterIsComplete pins the register's membership, so a later wave that adds an
// implementation has to move the name out of the unsupported list deliberately.
func TestCapabilitiesRegisterIsComplete(t *testing.T) {
	require.ElementsMatch(t, []scheduler.Capability{
		scheduler.CapNativeCut, scheduler.CapNativeVeto, scheduler.CapNativeEviction,
		scheduler.CapNativeODelta, scheduler.CapYoungDalyPacing,
		scheduler.CapSkiRentalWritePolicy, scheduler.CapChangepointForecast,
	}, scheduler.Capabilities())
}

// TestYoungDalyClauseIsOffUnderShippedDefaults is the behavioural half of "disabled by default":
// the pacing clause contributes nothing to a Decision produced from the shipped configuration and
// an ordinary above-soft-floor session, because no compaction cost has been measured yet. The
// breakdown says so by name rather than leaving a silent zero.
func TestYoungDalyClauseIsOffUnderShippedDefaults(t *testing.T) {
	in := scheduler.Inputs{
		Cfg:              config.Defaults().Scheduler,
		Now:              nowMilli,
		ContextTokens:    150_000,
		EffectiveWindow:  200_000,
		MaxOutputTokens:  8_000,
		LastCompactionTS: nowMilli - 3_600_000,
	}
	d := scheduler.Evaluate(in)
	require.NotContains(t, d.Reasons, scheduler.TriggerYoungDaly,
		"nothing measured delta, so the clause cannot fire")
	require.Equal(t, float64(1), d.Breakdown["young_daly_delta_unmeasured"],
		"and it says which guard held it, not nothing")
	require.Zero(t, d.YoungDalySeconds)
}

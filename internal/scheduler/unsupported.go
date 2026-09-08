package scheduler

import (
	"errors"
	"fmt"
	"slices"
	"sync/atomic"

	"github.com/qompack/qompack/internal/config"
)

// The scheduler's capability register (00-ARCHITECTURE.md §12.3, Qompack.md §2.4). Qompack sits
// BESIDE the host's own compaction; it does not drive it. Several behaviours that a reader of the
// design notes might expect from an "L3 scheduler" are therefore not merely unimplemented, they
// are unimplementable against the installed host, and a layer that quietly returned a plausible
// answer for one of them would be claiming a control it does not have.
//
// This file is how a caller finds that out: it asks, and it is told no, with a reason. Silence
// and a zero value are what this register exists to replace.
//
// Two classes live here and they answer differently:
//
//   - UNSUPPORTED. Native cut, native veto, native eviction and O(delta) native compaction. There
//     is no host surface for any of them. No configuration enables them; no future value of any
//     config key changes the answer. Require always fails.
//   - EXPERIMENTAL. Young-Daly pacing, the ski-rental write policy and changepoint forecasting.
//     The mathematics is present and unit-tested, but none of it is wired to act on its own, and
//     the register reports every one of them OFF unless a process explicitly opts in. The default
//     is off — not "off if you configure it off".

// ErrUnsupported is what a caller that asks for a behaviour this layer does not provide gets
// back. It is deliberately NOT core.ErrNotImplemented: "not implemented yet" invites a caller to
// wait for it, and three of the four unsupported capabilities are never arriving.
var ErrUnsupported = errors.New("scheduler: capability unsupported")

// Capability names a behaviour a caller may ask the scheduler for.
type Capability string

// The unsupported claims. Each is a control over the HOST's context window, and the host exposes
// none of them to a plugin.
const (
	// CapNativeCut is "cut the host's own context at a turn we choose". The host decides what its
	// summarization request contains; a PreCompact hook contributes instructions to it and nothing
	// more.
	CapNativeCut Capability = "native_cut"
	// CapNativeVeto is "refuse a compaction the host has decided to run". The hook has no veto:
	// its output is advisory and its exit code cannot cancel the host's action.
	CapNativeVeto Capability = "native_veto"
	// CapNativeEviction is "drop a specific block out of the live context window". Ranking
	// droppable content (see dropclass.go) is a RECOMMENDATION carried into the checkpoint; the
	// host's window is not addressable from here.
	CapNativeEviction Capability = "native_eviction"
	// CapNativeODelta is "make the host's own compaction cost O(delta)". O5 makes QOMPACK's
	// per-compaction work proportional to the unencoded residual; the host's summarization still
	// reads whatever the host reads.
	CapNativeODelta Capability = "native_o_delta_compaction"
)

// The experimental, default-off policies.
const (
	// CapYoungDalyPacing is the §6.7 optimal-checkpoint-interval clause acting on its own cadence.
	CapYoungDalyPacing Capability = "young_daly_pacing"
	// CapSkiRentalWritePolicy is the §5.6 cache-write decision (SkiRentalShouldWrite). Nothing in
	// this wave calls it.
	CapSkiRentalWritePolicy Capability = "ski_rental_write_policy"
	// CapChangepointForecast is BOCD used PREDICTIVELY — acting on a forecast rather than
	// reporting an observed run-length posterior.
	CapChangepointForecast Capability = "changepoint_forecast"
)

// unsupportedCaps and experimentalCaps are the register itself; Capabilities concatenates them.
var (
	unsupportedCaps  = []Capability{CapNativeCut, CapNativeVeto, CapNativeEviction, CapNativeODelta}
	experimentalCaps = []Capability{
		CapYoungDalyPacing, CapSkiRentalWritePolicy, CapChangepointForecast,
	}
)

// experimentalPolicies is the process-wide opt-in for the experimental class. It is an atomic and
// not a config key on purpose: a config default can be edited, shipped and forgotten, whereas
// nothing in the composition root calls EnableExperimentalPolicies at all, so the zero value —
// false, the default — is what every daemon runs with. TestExperimentalPoliciesAreOffByDefault
// asserts the zero value directly, without configuring anything off first.
var experimentalPolicies atomic.Bool

// ExperimentalPoliciesEnabled reports the opt-in's current state. False on a fresh process.
func ExperimentalPoliciesEnabled() bool { return experimentalPolicies.Load() }

// EnableExperimentalPolicies opts this process in. It is called by tests and by nothing in the
// shipped wiring; see the comment on experimentalPolicies.
func EnableExperimentalPolicies() { experimentalPolicies.Store(true) }

// DisableExperimentalPolicies restores the default. Tests defer it.
func DisableExperimentalPolicies() { experimentalPolicies.Store(false) }

// Support is the answer to one capability question: what was asked, whether it is available right
// now, and — always — why not.
type Support struct {
	Capability Capability
	Available  bool
	// Class is "unsupported", "experimental" or "unknown".
	Class string
	// Reason is a one-line explanation, non-empty whenever Available is false.
	Reason string
}

// The Support classes.
const (
	ClassUnsupported  = "unsupported"
	ClassExperimental = "experimental"
	ClassUnknown      = "unknown"
)

// Capabilities lists every capability this register knows about, unsupported first.
func Capabilities() []Capability {
	out := make([]Capability, 0, len(unsupportedCaps)+len(experimentalCaps))
	out = append(out, unsupportedCaps...)
	out = append(out, experimentalCaps...)
	return out
}

// Supports answers one capability question against cfg.
//
// An unrecognised capability is reported unavailable and ClassUnknown rather than accepted: a
// register that answered "sure" to a name it had never heard of would be worse than no register.
func Supports(c Capability, cfg config.SchedulerCfg) Support {
	switch {
	case slices.Contains(unsupportedCaps, c):
		return Support{
			Capability: c, Available: false, Class: ClassUnsupported,
			Reason: "the installed host exposes no such control; Qompack runs beside native compaction, not in place of it",
		}
	case slices.Contains(experimentalCaps, c):
		if !experimentalPolicies.Load() {
			return Support{
				Capability: c, Available: false, Class: ClassExperimental,
				Reason: "experimental policy, disabled by default; nothing in the shipped wiring enables it",
			}
		}
		if c == CapYoungDalyPacing && !cfg.YoungDaly.Enabled {
			return Support{
				Capability: c, Available: false, Class: ClassExperimental,
				Reason: "scheduler.youngDaly.enabled is false",
			}
		}
		return Support{Capability: c, Available: true, Class: ClassExperimental}
	default:
		return Support{
			Capability: c, Available: false, Class: ClassUnknown,
			Reason: "no such scheduler capability",
		}
	}
}

// Require is the enforcing form: it returns nil when the capability is available and an error
// wrapping ErrUnsupported, carrying the reason, when it is not. A caller that asks for a native
// cut gets this error, every time, whatever the configuration says.
func Require(c Capability, cfg config.SchedulerCfg) error {
	s := Supports(c, cfg)
	if s.Available {
		return nil
	}
	return fmt.Errorf("%s: %s: %w", string(c), s.Reason, ErrUnsupported)
}

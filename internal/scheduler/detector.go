package scheduler

import "github.com/qompack/qompack/internal/core"

// Detector is the BOCD (Bayesian online changepoint detection) seam (Qompack.md §6.6,
// 00-ARCHITECTURE.md §5.13). SP-12 owns the real posterior update; SP-01 ships NewBOCD returning
// a detector whose methods report core.ErrNotImplemented (or the documented zero value, for the
// two methods with no error return) so that wave-0 composition compiles today.
type Detector interface {
	// Observe folds one Features observation into the run-length posterior and returns the
	// resulting state.
	Observe(f Features) ChangepointState
	// State returns the most recently computed ChangepointState without observing anything new.
	State() ChangepointState
	// Reset clears the posterior back to its prior — for example, at a session boundary.
	Reset()
	// MarshalBinary serializes the detector's posterior for state/bocd.json.
	MarshalBinary() ([]byte, error)
	// UnmarshalBinary restores a previously serialized posterior.
	UnmarshalBinary(data []byte) error
}

// NewBOCD returns a stub BOCD Detector: constructing it always succeeds so wave-0 composition
// roots can wire a scheduler.Detector today, but every method reports core.ErrNotImplemented (or
// the documented zero ChangepointState, for the two methods with no error return) until SP-12
// lands the real posterior update (Qompack.md §6.6, 00-ARCHITECTURE.md §5.13).
func NewBOCD(hazardRate float64, features []string) Detector {
	return stubDetector{}
}

// stubDetector is the SP-01 placeholder Detector. SP-12 owns the real implementation.
type stubDetector struct{}

// Observe always reports the zero ChangepointState. Observe has no error return, and the zero
// value — run length 0, changepoint probability 0, AtChangepoint false, no posterior — is the
// only honest "no detection has happened yet" answer a stub can give.
func (stubDetector) Observe(f Features) ChangepointState { return ChangepointState{} }

// State always reports the zero ChangepointState, for the same reason as Observe.
func (stubDetector) State() ChangepointState { return ChangepointState{} }

// Reset is a no-op: there is no posterior yet to clear.
func (stubDetector) Reset() {}

// MarshalBinary always reports core.ErrNotImplemented.
func (stubDetector) MarshalBinary() ([]byte, error) { return nil, core.ErrNotImplemented }

// UnmarshalBinary always reports core.ErrNotImplemented.
func (stubDetector) UnmarshalBinary(data []byte) error { return core.ErrNotImplemented }

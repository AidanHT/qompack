package scheduler

// Detector is the BOCD (Bayesian online changepoint detection) seam (Qompack.md §6.6,
// 00-ARCHITECTURE.md §5.13). The implementation is bocd.go's Normal-Inverse-Gamma run-length
// posterior; NewBOCD is its only constructor. A Detector is a pure function of the observations it
// has been fed: it performs no I/O and reads no clock, so the same Features sequence always
// yields the same ChangepointState sequence and the same MarshalBinary bytes.
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

// NewBOCD returns the Qompack.md §6.6 changepoint Detector: an Adams–MacKay run-length posterior
// with constant hazard hazardRate (Appendix C changepoint.hazardRate) over the named feature
// streams (changepoint.features: "paths", "tools", "lexical", "time", "todos"). Construction
// always succeeds. A hazard outside (0,1) or NaN falls back to 1/bocdMaxRunLength —
// config.Validate normally prevents this; unknown feature names are dropped and duplicates
// collapsed; an empty selection falls back to the Appendix C default list.
func NewBOCD(hazardRate float64, features []string) Detector {
	return newBOCD(hazardRate, features)
}

package main

import (
	"path/filepath"

	"github.com/qompack/qompack/internal/eval"
)

// liveV7Rates is the rate table a task set with an efficiency block defaults to: rates.json stays a
// frozen material of the qompack-live-v1/v2 pre-registration, and rates-v7.json beside it adds the
// Stage A models' prices (amendment 4 item 9).
const liveV7Rates = "rates-v7.json"

// liveRatesFor is the rate table a run of ts reads: --rates when given, else, for a task set with an
// efficiency block, rates-v7.json beside the task set (coordinator ruling on review CLI-5), else the
// default. A model the table does not price is recorded per trial (estimate_unpriced) and noted; the
// run is not refused, since the decided cost C is the host's own figure, never this estimate.
func liveRatesFor(o liveOptions, ts eval.LiveTaskSet) string {
	if o.ratesGiven || ts.Analysis.Efficiency == nil {
		return o.rates
	}
	return filepath.Join(filepath.Dir(o.tasksFile), liveV7Rates)
}

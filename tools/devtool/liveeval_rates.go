package main

import (
	"fmt"
	"os"
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
// run is not refused, since the decided cost C is the host's own figure, never this estimate. A
// missing default table is refused with the way out named (review H-NEW-5).
func liveRatesFor(o liveOptions, ts eval.LiveTaskSet) (string, error) {
	if o.ratesGiven || ts.Analysis.Efficiency == nil {
		return o.rates, nil
	}
	p := filepath.Join(filepath.Dir(o.tasksFile), liveV7Rates)
	if _, err := os.Stat(p); err != nil {
		return "", fmt.Errorf("live-eval: task set %s has an efficiency block, so its rate table defaults to %s beside it, "+
			"which cannot be read (%w); pass --rates <table>", ts.ID, p, err)
	}
	return p, nil
}

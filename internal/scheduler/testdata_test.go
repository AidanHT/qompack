package scheduler

import (
	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
)

// Shared fixtures for the package's tests (plan "Shared fixtures" table). Commit 2 extends this
// file with the Inputs fixtures once the additive Inputs fields exist.

// baseNow is the fixed "now" every fixture is anchored to: 2023-11-14T22:13:20Z.
const baseNow core.UnixMilli = 1_700_000_000_000

// baseCfg is the scheduler section of config.Defaults(); tests copy and mutate it.
func baseCfg() config.SchedulerCfg { return config.Defaults().Scheduler }

// ptr returns a pointer to v (for the *float64 nil-means-measure contract).
func ptr(v float64) *float64 { return &v }

package daemon

// The schedulertest conformance suites (00-ARCHITECTURE.md §5.22) against the REAL runtime and
// the REAL detector: RunSchedulerSuite over a fresh daemon-owned scheduler.Runtime per factory
// call — fake clock, in-memory store/graph doubles, a temp project root so Persist writes real
// state files — and RunDetectorSuite over scheduler.NewBOCD at Appendix C's shape.
//
// Sequential (no t.Parallel): every factory call flips the process-wide p-selection gate.

import (
	"testing"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/scheduler"
	"github.com/qompack/qompack/internal/scheduler/schedulertest"
)

func TestSchedulerSuite_DaemonRuntime(t *testing.T) {
	defer scheduler.DisablePSelection()
	schedulertest.RunSchedulerSuite(t, "daemon", func(t *testing.T) scheduler.Runtime {
		fx := newRTFixture(t)
		fx.bind(rtSession)
		return fx.rt
	})
}

func TestSchedulerSuite_DaemonRuntimeUnbound(t *testing.T) {
	defer scheduler.DisablePSelection()
	schedulertest.RunSchedulerSuite(t, "daemon-unbound", func(t *testing.T) scheduler.Runtime {
		return newRTFixture(t).rt
	})
}

func TestDetectorSuite_BOCD(t *testing.T) {
	cp := config.Defaults().Scheduler.Changepoint
	schedulertest.RunDetectorSuite(t, "bocd", func(*testing.T) scheduler.Detector {
		return scheduler.NewBOCD(cp.HazardRate, cp.Features)
	})
}

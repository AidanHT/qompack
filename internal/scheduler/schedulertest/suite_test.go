package schedulertest

import (
	"context"
	"encoding/binary"
	"fmt"
	"testing"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/scheduler"
)

// minimalRuntime is the smallest NON-stub scheduler.Runtime: Evaluate runs the real, pure
// scheduler.Evaluate over this package's own fixture, Observe delegates to minimalDetector, and
// NotifyActivity/IdleSince share one timestamp. It exists to exercise RunSchedulerSuite end to
// end inside this package; the real-Runtime and real-Detector suite runs are internal/daemon's.
type minimalRuntime struct {
	det  scheduler.Detector
	last core.UnixMilli
}

func newMinimalRuntime() *minimalRuntime { return &minimalRuntime{det: newMinimalDetector()} }

func (m *minimalRuntime) Observe(_ context.Context, f scheduler.Features, _ core.TurnIndex) scheduler.ChangepointState {
	return m.det.Observe(f)
}

func (m *minimalRuntime) Evaluate(context.Context) (scheduler.Decision, error) {
	return scheduler.Evaluate(baseInputs()), nil
}

func (m *minimalRuntime) NotifyActivity(ts core.UnixMilli) { m.last = ts }

func (m *minimalRuntime) IdleSince() (core.UnixMilli, bool) { return m.last, m.last > 0 }

func (m *minimalRuntime) Persist(context.Context) error { return nil }

// minimalDetector is a scheduler.Detector whose posterior is always the single-hypothesis
// distribution [1]: it counts observations as its run length and round-trips that count through
// MarshalBinary. The real BOCD posterior update is A1's bocd.go.
type minimalDetector struct {
	observed int
}

func newMinimalDetector() *minimalDetector { return &minimalDetector{} }

func (d *minimalDetector) state() scheduler.ChangepointState {
	return scheduler.ChangepointState{RunLength: d.observed, Posterior: []float64{1}}
}

func (d *minimalDetector) Observe(scheduler.Features) scheduler.ChangepointState {
	d.observed++
	return d.state()
}

func (d *minimalDetector) State() scheduler.ChangepointState { return d.state() }

func (d *minimalDetector) Reset() { d.observed = 0 }

func (d *minimalDetector) MarshalBinary() ([]byte, error) {
	return binary.BigEndian.AppendUint64(nil, uint64(d.observed)), nil
}

func (d *minimalDetector) UnmarshalBinary(b []byte) error {
	if len(b) != 8 {
		return fmt.Errorf("minimalDetector: %d bytes, want 8: %w", len(b), core.ErrNotFound)
	}
	d.observed = int(binary.BigEndian.Uint64(b))
	return nil
}

// TestRunSchedulerSuite_MinimalRuntime runs the whole suite — shape and behaviour, nothing
// skipped — against the minimal runtime, proving the suite itself is sound before the daemon's
// real Runtime is pointed at it.
func TestRunSchedulerSuite_MinimalRuntime(t *testing.T) {
	RunSchedulerSuite(t, "minimal", func(*testing.T) scheduler.Runtime {
		return newMinimalRuntime()
	})
}

// TestRunDetectorSuite_MinimalDetector runs the detector suite against the minimal detector for
// the same reason.
func TestRunDetectorSuite_MinimalDetector(t *testing.T) {
	RunDetectorSuite(t, "minimal", func(*testing.T) scheduler.Detector {
		return newMinimalDetector()
	})
}

package checkpoint

import (
	"sync"
	"time"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/obs"
	"github.com/qompack/qompack/internal/paths"
)

// The package-level observers.
//
// 00-ARCHITECTURE.md §5.14 fixes ExtractDecisions, Truncate, ValidatePointers and
// StripInjectionsCount as package-level functions taking neither a logger nor a registry, yet each
// has to emit counters and Warn logs. Rather than amend §5.14 or smuggle a text-carrying field
// into SourceSet — which would undo the very invariant SourceSet exists to enforce — the package
// keeps one set of observers behind an RWMutex.
//
// Both default to no-ops, so every receiver-less function in this package is usable, and
// unit-testable, with no writer constructed. OpenWriter calls SetObservers once.
var (
	obsMu  sync.RWMutex
	curLog logging.Logger = logging.Nop()
	curReg obs.Registry   = nopRegistry{}
)

// SetObservers installs the package-level logger and metrics registry. It is safe to call
// repeatedly; the last write wins. OpenWriter calls it once, so a process that has constructed a
// writer sees the same observers from the receiver-less functions as from the writer's own
// methods.
func SetObservers(log logging.Logger, m obs.Registry) {
	obsMu.Lock()
	defer obsMu.Unlock()
	if log == nil {
		log = logging.Nop()
	}
	if m == nil {
		m = nopRegistry{}
	}
	curLog, curReg = log, m
}

// pkgLog returns the package-level logger. It never returns nil.
func pkgLog() logging.Logger {
	obsMu.RLock()
	defer obsMu.RUnlock()
	return curLog
}

// pkgMetrics returns the package-level registry. It never returns nil.
func pkgMetrics() obs.Registry {
	obsMu.RLock()
	defer obsMu.RUnlock()
	return curReg
}

// nopRegistry is the default obs.Registry: every instrument discards, Snapshot is empty and
// CheckBudgets reports nothing. It exists so that a caller who has not constructed a writer — a
// unit test, or SP-13 reaching ExtractDecisions directly — cannot nil-dereference its way through
// a counter.
type nopRegistry struct{}

func (nopRegistry) Hist(string) obs.Histogram { return nopHist{} }
func (nopRegistry) Counter(string) obs.Counter {
	return nopCounter{}
}
func (nopRegistry) Gauge(string) obs.Gauge { return nopGauge{} }

// Snapshot returns an empty snapshot with initialized maps, so a caller may range over it without
// a nil check.
func (nopRegistry) Snapshot() obs.Snapshot {
	return obs.Snapshot{
		Hists:    map[string]obs.HistSnapshot{},
		Counters: map[string]int64{},
		Gauges:   map[string]int64{},
	}
}

// CheckBudgets reports no breaches: a discarding registry has observed nothing to breach with.
func (nopRegistry) CheckBudgets(config.Config) []obs.BudgetBreach { return nil }

// Persist writes nothing and reports success. A no-op registry has no snapshot worth a file, and
// returning an error would make an unobserved process louder than an observed one.
func (nopRegistry) Persist(paths.Layout) error { return nil }

type nopHist struct{}

func (nopHist) Observe(time.Duration)      {}
func (nopHist) Snapshot() obs.HistSnapshot { return obs.HistSnapshot{} }
func (nopHist) Reset()                     {}

type nopCounter struct{}

func (nopCounter) Add(int64)    {}
func (nopCounter) Value() int64 { return 0 }

type nopGauge struct{}

func (nopGauge) Set(int64)    {}
func (nopGauge) Add(int64)    {}
func (nopGauge) Value() int64 { return 0 }

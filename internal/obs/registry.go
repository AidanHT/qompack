package obs

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
)

// Snapshot is a point-in-time read of every instrument a Registry holds. It is what
// Registry.Snapshot returns, what Persist writes to metrics/latency.json, and what
// /qompack:status (SP-14) renders.
type Snapshot struct {
	TS       core.UnixMilli          `json:"ts"`
	Hists    map[string]HistSnapshot `json:"hists"`
	Counters map[string]int64        `json:"counters"`
	Gauges   map[string]int64        `json:"gauges"`
}

// BudgetBreach reports one gated budget currently over its configured limit.
type BudgetBreach struct {
	Budget   string
	Observed time.Duration
	Limit    time.Duration
	// Windows is the number of consecutive CheckBudgets calls, including this one, that have
	// found Budget over limit. The daemon's real degrade-to-spool decision (§8.1, §2.4) fires at
	// Windows == 3; CheckBudgets itself only counts and reports, it never degrades anything.
	Windows int
}

// Registry is the lazily-populated set of instruments one process (a hook client, the daemon, the
// MCP server) shares. Every accessor creates its instrument on first use, so callers never need a
// separate registration step.
type Registry interface {
	Hist(name string) Histogram
	Counter(name string) Counter
	Gauge(name string) Gauge
	// Snapshot returns a deep copy: mutating the result never affects the Registry.
	Snapshot() Snapshot
	// CheckBudgets evaluates every GATED budget's current percentile against cfg and returns one
	// BudgetBreach per budget presently over limit. It is stateful: Windows counts consecutive
	// over-limit calls per budget, maintained inside the Registry across calls.
	CheckBudgets(cfg config.Config) []BudgetBreach
	// Persist writes the current Snapshot to l.Metrics/latency.json via paths.WriteAtomic.
	Persist(l paths.Layout) error
}

// latencyFileName is metrics/latency.json's basename (00-ARCHITECTURE.md §3.3).
const latencyFileName = "latency.json"

// filePerm is the permission WriteAtomic applies to metrics/latency.json.
const filePerm = 0o600

type registry struct {
	clock core.Clock

	mu       sync.RWMutex
	hists    map[string]*histogram
	counters map[string]*counter
	gauges   map[string]*gauge

	breachMu     sync.Mutex
	breachStreak map[BudgetID]int
}

// New returns an empty Registry. clock is used only for Snapshot's TS field.
func New(clock core.Clock) Registry {
	return &registry{
		clock:        clock,
		hists:        make(map[string]*histogram),
		counters:     make(map[string]*counter),
		gauges:       make(map[string]*gauge),
		breachStreak: make(map[BudgetID]int),
	}
}

func (r *registry) Hist(name string) Histogram {
	r.mu.Lock()
	defer r.mu.Unlock()
	h, ok := r.hists[name]
	if !ok {
		h = newHistogram()
		r.hists[name] = h
	}
	return h
}

func (r *registry) Counter(name string) Counter {
	r.mu.Lock()
	defer r.mu.Unlock()
	c, ok := r.counters[name]
	if !ok {
		c = newCounter()
		r.counters[name] = c
	}
	return c
}

func (r *registry) Gauge(name string) Gauge {
	r.mu.Lock()
	defer r.mu.Unlock()
	g, ok := r.gauges[name]
	if !ok {
		g = newGauge()
		r.gauges[name] = g
	}
	return g
}

func (r *registry) Snapshot() Snapshot {
	r.mu.RLock()
	defer r.mu.RUnlock()

	snap := Snapshot{
		TS:       core.NowMilli(r.clock),
		Hists:    make(map[string]HistSnapshot, len(r.hists)),
		Counters: make(map[string]int64, len(r.counters)),
		Gauges:   make(map[string]int64, len(r.gauges)),
	}
	for name, h := range r.hists {
		snap.Hists[name] = h.Snapshot()
	}
	for name, c := range r.counters {
		snap.Counters[name] = c.Value()
	}
	for name, g := range r.gauges {
		snap.Gauges[name] = g.Value()
	}
	return snap
}

func (r *registry) CheckBudgets(cfg config.Config) []BudgetBreach {
	r.breachMu.Lock()
	defer r.breachMu.Unlock()

	var breaches []BudgetBreach
	for _, b := range Budgets() {
		if !b.Gated {
			continue
		}
		snap := r.Hist(b.Hist).Snapshot()
		limit := b.Limit(cfg)
		observed := percentileOfSnapshot(snap, b.Pct)

		if snap.N > 0 && observed > limit {
			r.breachStreak[b.ID]++
		} else {
			r.breachStreak[b.ID] = 0
		}
		if streak := r.breachStreak[b.ID]; streak > 0 {
			breaches = append(breaches, BudgetBreach{
				Budget: string(b.ID), Observed: observed, Limit: limit, Windows: streak,
			})
		}
	}
	return breaches
}

func (r *registry) Persist(l paths.Layout) error {
	b, err := json.Marshal(r.Snapshot())
	if err != nil {
		return fmt.Errorf("obs: Persist: marshal snapshot: %w", err)
	}
	p := filepath.Join(l.Metrics, latencyFileName)
	if err := paths.WriteAtomic(p, b, filePerm); err != nil {
		return fmt.Errorf("obs: Persist: write %s: %w", p, err)
	}
	return nil
}

// Timed runs f, recording its wall-clock duration into h regardless of whether f returns an
// error, and returns f's error unchanged.
func Timed(h Histogram, f func() error) error {
	start := time.Now()
	err := f()
	h.Observe(time.Since(start))
	return err
}

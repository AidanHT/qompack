package eval

import (
	"time"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/obs"
)

// Latency-model coefficients. They are calibrated against two sentences of the design, not tuned:
// see LatencyModel's doc comment and TestReplay_LatencyModelAnchors, which names the sentence each
// one answers to. They are not Qompack tunables — nothing at runtime reads them — so they are
// constants here rather than config keys.
const (
	defaultPauseBaseMS           = 3000
	defaultPausePerKResidualMS   = 150
	defaultFirstTurnBaseMS       = 800
	defaultFirstTurnPerKRehydrMS = 90
)

// New returns a Harness. Every zero-valued member of o is replaced by a safe default, so
// eval.New(eval.Options{}) is always valid. New registers no policies and mutates no globals.
func New(o Options) Harness {
	h := &harness{
		cfg:     o.Cfg,
		log:     o.Log,
		metrics: o.Metrics,
		clock:   o.Clock,
		lat:     o.Latency,
		pool:    map[string]latencyPool{},
	}
	if h.cfg.Eval.MinSessions == 0 {
		h.cfg = config.Defaults()
	}
	if h.log == nil {
		h.log = logging.Nop()
	}
	if h.clock == nil {
		h.clock = core.SystemClock()
	}
	if h.metrics == nil {
		h.metrics = obs.New(h.clock)
	}
	if !h.lat.Modelled {
		h.lat = DefaultLatencyModel()
	}
	return h
}

// DefaultLatencyModel returns the calibrated model. Modelled is true, which is both the honest
// answer and what New uses to tell a configured model from a zero value.
func DefaultLatencyModel() LatencyModel {
	return LatencyModel{
		PauseBaseMS:           defaultPauseBaseMS,
		PausePerKResidualMS:   defaultPausePerKResidualMS,
		FirstTurnBaseMS:       defaultFirstTurnBaseMS,
		FirstTurnPerKRehydrMS: defaultFirstTurnPerKRehydrMS,
		Modelled:              true,
	}
}

// SetLiveRunner installs the §6.3-tier-3 runner. It is declared on the concrete type and reached
// through a type assertion, so the §5.18 Harness interface gains no method (Rule W-3).
func (h *harness) SetLiveRunner(r LiveRunner) { h.live = r }

// observe records d against the named histogram. It is the only use this package makes of obs.
func (h *harness) observe(name string, start time.Time) {
	h.metrics.Hist(name).Observe(h.clock.Since(start))
}

// Load, Replay, Compare, Belady, ScoreRun and Report are implemented in replay.go, divergence.go,
// belady.go and score.go respectively.

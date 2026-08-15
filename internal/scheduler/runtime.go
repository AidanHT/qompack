package scheduler

import (
	"context"

	"github.com/qompack/qompack/internal/core"
)

// Runtime is the stateful wrapper the daemon owns around Evaluate and Detector
// (00-ARCHITECTURE.md §5.13). Its implementation assembles Inputs.Candidates from the dependence
// DAG and the content-addressed store's tool-use records, neither of which this package may
// import (00-ARCHITECTURE.md §3.2), so the concrete type lives in internal/daemon — a
// composition root — and is owned by SP-12 alongside the rest of this package's real behaviour.
//
// SP-01 declares the interface only. Deliberately, there is no in-package stub constructor for
// it (unlike, for example, store.Open): internal/scheduler must stay pure, and a Runtime
// implementation is inherently stateful and I/O-bound (it persists to state/bocd.json and
// state/scheduler.json). Callers that need a Runtime value before SP-12 lands — this package's
// own conformance suite, for instance — supply their own minimal implementation.
type Runtime interface {
	// Observe folds one Features observation, taken at turn at, into the scheduler's BOCD state
	// and returns the resulting ChangepointState.
	Observe(ctx context.Context, f Features, at core.TurnIndex) ChangepointState
	// Evaluate runs the composite trigger against the Runtime's current state and returns its
	// Decision.
	Evaluate(ctx context.Context) (Decision, error)
	// NotifyActivity records that the session was active at ts, resetting the idle clock.
	NotifyActivity(ts core.UnixMilli)
	// IdleSince reports the timestamp the session became idle, if it is currently idle.
	IdleSince() (core.UnixMilli, bool)
	// Persist writes the Runtime's durable state (state/bocd.json, state/scheduler.json).
	Persist(ctx context.Context) error
}

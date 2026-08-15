package analyzer

import (
	"context"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/dag"
	"github.com/qompack/qompack/internal/store"
)

// DeltaScorer prices blocks against the observed continuation (00-ARCHITECTURE.md §5.12): how
// much worse the continuation would have gone without each block. Mode names which cost tier the
// implementation is, so /qompack:status and the eval harness can report what was actually run.
type DeltaScorer interface {
	// Mode reports this scorer's cost tier.
	Mode() DeltaMode
	// Score returns a Δ(c) proxy in [0,1] for each block, measured against the OBSERVED
	// continuation. Every block passed in gets an entry.
	Score(ctx context.Context, blocks []Block, continuation Continuation) (map[dag.NodeID]float64, error)
}

// NewCheapScorer returns the cheap-tier Δ-scorer of 00-ARCHITECTURE.md §5.12: token overlap plus
// symbol-reference counting, reading block content back out of s. Constructing always succeeds,
// so wave-0 composition roots can wire a DeltaScorer today, but Score reports
// core.ErrNotImplemented until SP-15 lands the real scorer.
func NewCheapScorer(s store.Store) DeltaScorer { return stubScorer{} }

// stubScorer is the SP-01 placeholder DeltaScorer. SP-15 owns the real implementation.
type stubScorer struct{}

// Mode returns DeltaCheap. Mode has no error return, and the honest answer is not a zero value
// here but the tier this scorer is contractually the constructor for: NewCheapScorer's caller
// asked for the cheap tier, and reporting anything else — including "" — would misdescribe which
// implementation is wired in, which is the one thing Mode exists to tell /qompack:status.
func (stubScorer) Mode() DeltaMode { return DeltaCheap }

// Score always reports core.ErrNotImplemented.
func (stubScorer) Score(ctx context.Context, blocks []Block, continuation Continuation) (map[dag.NodeID]float64, error) {
	return nil, core.ErrNotImplemented
}

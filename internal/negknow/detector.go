package negknow

import (
	"context"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/dag"
)

// Detector is negative-knowledge discovery source #3 (00-ARCHITECTURE.md §5.10, §8.3): the
// heuristic that infers an (unreported) elimination from a test-fail -> revert ->
// different-approach pattern in the dependence DAG, producing Records with Source ==
// SourceHeuristic.
//
// 00-ARCHITECTURE.md §5.10 declares this interface without giving it a constructor: unlike Ledger
// (Open) or the other stubbed interfaces in this tree, nothing in wave 0 needs to hold a
// constructed Detector value (daemon.Options names a negknow.Ledger, not a Detector), and the
// heuristic's own construction parameters are not yet decided. SP-01 therefore declares the
// interface — the "real declaration" §14.1 requires — without inventing an unspecified
// constructor function; SP-09 adds one alongside its real implementation.
type Detector interface {
	// Scan walks g looking for the test-fail -> revert -> different-approach pattern at or after
	// since, returning one Record per inferred elimination.
	Scan(ctx context.Context, g dag.Graph, since core.TurnIndex) ([]Record, error)
}

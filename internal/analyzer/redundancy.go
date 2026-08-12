package analyzer

import (
	"context"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/store"
)

// DetectRedundancy scans session sess in s for two kinds of waste: tool results a later tool use
// on the same path superseded, and tool results that are near-duplicates of one another under the
// configured MinHash Jaccard threshold (00-ARCHITECTURE.md §5.12, §8.1).
//
// It is a read-only analysis: it reports what is redundant and never marks, drops or rewrites
// anything itself. store.MarkSuperseded is the caller's call to make. Always reports
// core.ErrNotImplemented until SP-15 lands.
func DetectRedundancy(ctx context.Context, s store.Store, sess core.SessionID) (RedundancyReport, error) {
	return RedundancyReport{}, core.ErrNotImplemented
}

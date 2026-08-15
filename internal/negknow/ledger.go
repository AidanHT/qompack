package negknow

import (
	"context"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/dag"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/obs"
	"github.com/qompack/qompack/internal/sketch"
	"github.com/qompack/qompack/internal/store"
)

// Deps is the set of collaborators Open assembles a Ledger from (00-ARCHITECTURE.md §14.0 of
// plans/V1-SP-01-foundation-toolchain-and-contracts.md: §5.10's Open references this type without
// defining it; SP-01 defines it here).
type Deps struct {
	Store store.Store
	Graph dag.Graph

	Log     logging.Logger
	Metrics obs.Registry
	Clock   core.Clock
}

// Health summarizes the ledger's current size and the tried.bloom filter's saturation
// (00-ARCHITECTURE.md §5.10): what `/qompack:status` reads to decide whether a rebuild-with-resize
// is due (§11.4, §12 "Bloom saturation").
type Health struct {
	Records, Active, Stale int
	FillRatio, EstFPRate   float64
	NeedsResize            bool
}

// Ledger is the negative-knowledge elimination store's full seam (00-ARCHITECTURE.md §5.10):
// recording eliminations, answering the three-way already_tried question, tracking staleness
// against the store's current file versions, and rebuilding tried.bloom from active records only.
type Ledger interface {
	// Record appends r to records/eliminations.jsonl and updates tried.bloom, returning r's
	// assigned ID.
	Record(ctx context.Context, r Record) (string, error)
	// Query answers the three-way already_tried question for target/approach at scope.
	Query(ctx context.Context, target, approach string, scope Scope) (Answer, error)
	// Get looks up a Record by ID.
	Get(ctx context.Context, id string) (Record, error)
	// Active returns every StatusActive Record visible at scope.
	Active(ctx context.Context, scope Scope) ([]Record, error)
	// All returns every Record ever appended, regardless of Status.
	All(ctx context.Context) ([]Record, error)
	// MarkStale flips every Record in ids to StatusStale, recording because as StaleBecause.
	MarkStale(ctx context.Context, ids []string, because []string) error
	// RefreshStaleness compares every active record's depends_on hashes against s's current file
	// versions and flips changed ones to stale. It returns the flipped ids.
	RefreshStaleness(ctx context.Context, s store.Store) ([]string, error)
	// RebuildBloom rebuilds tried.bloom from ACTIVE RECORDS ONLY — never from a checkpoint, never
	// from context (00-ARCHITECTURE.md §3.3, §13 invariant 2). It resizes if
	// sketch.Bloom.ResizeTarget says so.
	RebuildBloom(ctx context.Context) (*sketch.Bloom, Health, error)
	// Health reports the ledger's current size and bloom saturation.
	Health() Health
	// Close releases every resource this Ledger holds.
	Close() error
}

// Open returns a Ledger rooted at root, backed by the bloom b. Constructing always succeeds, so
// wave-0 composition roots can wire a negknow.Ledger today, but every operation is a stub until
// SP-09 lands the real elimination ledger (00-ARCHITECTURE.md §5.10): every method with an error
// return reports core.ErrNotImplemented, and Health — which has no error return — reports the
// documented zero value.
func Open(root string, cfg config.Config, b *sketch.Bloom, deps Deps) (Ledger, error) {
	return stubLedger{}, nil
}

// stubLedger is the SP-01 placeholder Ledger. SP-09 owns the real implementation.
type stubLedger struct{}

// Record always reports core.ErrNotImplemented.
func (stubLedger) Record(ctx context.Context, r Record) (string, error) {
	return "", core.ErrNotImplemented
}

// Query always reports core.ErrNotImplemented.
func (stubLedger) Query(ctx context.Context, target, approach string, scope Scope) (Answer, error) {
	return Answer{}, core.ErrNotImplemented
}

// Get always reports core.ErrNotImplemented.
func (stubLedger) Get(ctx context.Context, id string) (Record, error) {
	return Record{}, core.ErrNotImplemented
}

// Active always reports core.ErrNotImplemented.
func (stubLedger) Active(ctx context.Context, scope Scope) ([]Record, error) {
	return nil, core.ErrNotImplemented
}

// All always reports core.ErrNotImplemented.
func (stubLedger) All(ctx context.Context) ([]Record, error) {
	return nil, core.ErrNotImplemented
}

// MarkStale always reports core.ErrNotImplemented.
func (stubLedger) MarkStale(ctx context.Context, ids []string, because []string) error {
	return core.ErrNotImplemented
}

// RefreshStaleness always reports core.ErrNotImplemented.
func (stubLedger) RefreshStaleness(ctx context.Context, s store.Store) ([]string, error) {
	return nil, core.ErrNotImplemented
}

// RebuildBloom always reports core.ErrNotImplemented.
func (stubLedger) RebuildBloom(ctx context.Context) (*sketch.Bloom, Health, error) {
	return nil, Health{}, core.ErrNotImplemented
}

// Health always returns the zero Health. Health has no error return, so the zero value — no
// records, no bloom saturation to report — is Rule 1's documented answer: a stub ledger has
// recorded nothing.
func (stubLedger) Health() Health { return Health{} }

// Close always reports core.ErrNotImplemented.
func (stubLedger) Close() error { return core.ErrNotImplemented }

package pins

import (
	"context"

	"github.com/qompack/qompack/internal/core"
)

// Invariant is a single pinned fact: user- or agent-asserted text that must never be summarized,
// truncated, or regenerated (Qompack.md §7.4, 00-ARCHITECTURE.md §5.14). It is defined here and
// aliased by checkpoint.Invariant — see the package comment for why.
//
// The json tags are explicit and frozen: this struct serializes into checkpoints/*.json (the
// "invariants" array) and into pins/invariants.jsonl add records, and
// testdata/golden/contracts/checkpoint/want/0001.json plus
// testdata/golden/contracts/pins/want/{add,tombstone}.jsonl are frozen fixtures (Rule W-2) that
// use exactly these spellings. Do not change them without an amendment.
type Invariant struct {
	// ID is the invariant's stable identifier, for example "inv_7c1a9e2f4b60".
	ID string `json:"id"`
	// Text is the pinned text itself, verbatim.
	Text string `json:"text"`
	// Source names who pinned it: "user" or "agent".
	Source string `json:"source"`
	// Pinned is when this invariant was added.
	Pinned core.UnixMilli `json:"pinned"`
}

// Store is the pins/invariants.json seam (00-ARCHITECTURE.md §5.14). Every operation is
// append-only at the log level: Add appends, Remove writes a tombstone record rather than
// deleting anything, and Materialize regenerates the pins/invariants.json convenience view from
// the log without ever becoming the source of truth itself (00-ARCHITECTURE.md §3.3).
type Store interface {
	// Add appends inv to the log. Adding an ID that already exists is the caller's error to
	// avoid, not this method's to detect — the log has no uniqueness index.
	Add(ctx context.Context, inv Invariant) error
	// Remove appends a tombstone record for id. It never rewrites or deletes the original add
	// record.
	Remove(ctx context.Context, id string) error
	// All returns every invariant that has not been tombstoned, in append order.
	All(ctx context.Context) ([]Invariant, error)
	// Materialize regenerates the pins/invariants.json view from the append-only log.
	Materialize(ctx context.Context) error
}

// Open returns a stub pins.Store rooted at root: constructing it always succeeds so wave-0
// composition roots can wire a pins.Store today, but every operation reports
// core.ErrNotImplemented until SP-10 lands the real append-only log (00-ARCHITECTURE.md §5.14).
// Unlike store.Open, Open takes no config.Config: nothing pins.Store does today is
// config-tunable, and internal/pins/pinstest's own conformance suite must be able to construct a
// Store without importing internal/config (00-ARCHITECTURE.md §3.2 — a <pkg>test subpackage's
// allow-set is its own package, testutil, and core only).
func Open(root string) (Store, error) {
	return stubStore{}, nil
}

// stubStore is the SP-01 placeholder Store. SP-10 owns the real implementation.
type stubStore struct{}

// Add always reports core.ErrNotImplemented.
func (stubStore) Add(ctx context.Context, inv Invariant) error { return core.ErrNotImplemented }

// Remove always reports core.ErrNotImplemented.
func (stubStore) Remove(ctx context.Context, id string) error { return core.ErrNotImplemented }

// All always reports core.ErrNotImplemented.
func (stubStore) All(ctx context.Context) ([]Invariant, error) { return nil, core.ErrNotImplemented }

// Materialize always reports core.ErrNotImplemented.
func (stubStore) Materialize(ctx context.Context) error { return core.ErrNotImplemented }

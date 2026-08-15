package sketch

import (
	"iter"

	"github.com/qompack/qompack/internal/core"
)

// Bloom is the membership sketch behind sketches/tried.bloom (00-ARCHITECTURE.md §5.7,
// Appendix A). Per §13 invariant 3, a bloom filter is never the source of truth: every membership
// answer must be backed by a record lookup or explicitly flagged BloomOnly, which is why Test's
// stub answer can never become a silent false positive — see Test's own doc comment.
type Bloom struct {
	capacity int
	fpRate   float64
}

// NewBloom returns a Bloom sized for capacity expected entries at fpRate false-positive rate.
// Constructing always succeeds — no bit array is allocated by the stub, since Add is a no-op —
// so wave-0 composition roots can wire a sketch.Bloom today, but every operation is a stub until
// SP-03 lands the real m = -n·ln(p)/(ln2)², k = (m/n)·ln2 sizing and k-hash-function
// implementation (00-ARCHITECTURE.md §5.7).
func NewBloom(capacity int, fpRate float64) *Bloom {
	return &Bloom{capacity: capacity, fpRate: fpRate}
}

// Add is a no-op: the stub has no bit array to set bits in, and Add has no return value at all to
// signal otherwise.
func (b *Bloom) Add(key []byte) {}

// Test always reports false in the stub: a bloom that claimed membership would be a FALSE
// POSITIVE in the one direction the design forbids (§13 invariant 3 — a bloom filter is never the
// source of truth, and every membership answer must be backed by a record lookup or explicitly
// flagged BloomOnly). false is always a safe answer for a filter nothing has ever been added to;
// true never would be.
func (b *Bloom) Test(key []byte) bool { return false }

// Count always returns 0: Add never records anything in the stub.
func (b *Bloom) Count() int { return 0 }

// FillRatio always returns 0: with nothing added, the (nonexistent) bit array is not fuller than
// empty.
func (b *Bloom) FillRatio() float64 { return 0 }

// EstimatedFPRate always returns 0: with nothing added, there is no observed false-positive rate
// to estimate.
func (b *Bloom) EstimatedFPRate() float64 { return 0 }

// Capacity returns the capacity and false-positive rate b was constructed with. Unlike this
// type's other accessors, these are not computed from any operation — they are simply NewBloom's
// own arguments, echoed back — so reporting them for real does not fake any behaviour.
func (b *Bloom) Capacity() (n int, fp float64) { return b.capacity, b.fpRate }

// RebuildBloom returns a fresh Bloom sized for capacity and fpRate. keys is never iterated in the
// stub: SP-03's real implementation adds every key keys yields, but until then there is no bit
// array for Add to populate, so walking keys would do work for no observable effect.
func RebuildBloom(capacity int, fpRate float64, keys iter.Seq[[]byte]) *Bloom {
	return NewBloom(capacity, fpRate)
}

// ResizeTarget always reports (current capacity, current fpRate, false): the stub's FillRatio is
// always 0, so it never has grounds to say a resize is needed.
func (b *Bloom) ResizeTarget() (capacity int, fp float64, needed bool) {
	return b.capacity, b.fpRate, false
}

// Header always returns the zero Header. Header has no error return, so the stub reports Rule 1's
// documented zero value: it carries no real format metadata until SP-03 lands real Save/Load.
func (b *Bloom) Header() Header { return Header{} }

// MarshalBinary always reports core.ErrNotImplemented.
func (b *Bloom) MarshalBinary() ([]byte, error) { return nil, core.ErrNotImplemented }

// UnmarshalBinary always reports core.ErrNotImplemented.
func (b *Bloom) UnmarshalBinary(data []byte) error { return core.ErrNotImplemented }

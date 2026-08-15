package sketch

import "github.com/qompack/qompack/internal/core"

// Counted is one key and its (estimated or exact, depending on the source) count.
type Counted struct {
	Key   string
	Count int
}

// MisraGries is the top-k heavy-hitter counter (00-ARCHITECTURE.md §5.7): by construction it
// never reports a false positive — every key Top returns really is among the most frequent seen,
// though it may under-report a true heavy hitter's count.
type MisraGries struct {
	k int
}

// NewMisraGries returns a MisraGries tracking up to k candidate keys. Constructing always
// succeeds — no candidate table is allocated by the stub, since Add is a no-op — so wave-0
// composition roots can wire a sketch.MisraGries today, but every operation is a stub until
// SP-03 lands the real implementation.
func NewMisraGries(k int) *MisraGries {
	return &MisraGries{k: k}
}

// Add is a no-op: the stub has no candidate table to update, and Add has no return value at all
// to signal otherwise.
func (m *MisraGries) Add(key string, n int) {}

// Top always returns nil: with nothing added, there is nothing to report — and, per Misra-Gries's
// own "no false positives, by construction" guarantee, an empty result is never itself a false
// positive.
func (m *MisraGries) Top(n int) []Counted { return nil }

// MergeFrom always reports core.ErrNotImplemented.
func (m *MisraGries) MergeFrom(o *MisraGries) error { return core.ErrNotImplemented }

// Header always returns the zero Header (see Bloom.Header's comment for why).
func (m *MisraGries) Header() Header { return Header{} }

// MarshalBinary always reports core.ErrNotImplemented.
func (m *MisraGries) MarshalBinary() ([]byte, error) { return nil, core.ErrNotImplemented }

// UnmarshalBinary always reports core.ErrNotImplemented.
func (m *MisraGries) UnmarshalBinary(data []byte) error { return core.ErrNotImplemented }

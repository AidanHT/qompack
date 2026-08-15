package sketch

import "github.com/qompack/qompack/internal/core"

// HLL is the HyperLogLog cardinality sketch behind sketches/explore.hll (00-ARCHITECTURE.md
// §5.7): an approximate count of distinct paths/symbols explored this session.
type HLL struct {
	registers int
}

// NewHLL returns an HLL with the given register count (2048 registers gives roughly 2KB and
// roughly 2.3% error — see 00-ARCHITECTURE.md §5.7). Constructing always succeeds — no register
// array is allocated by the stub, since Add is a no-op — so wave-0 composition roots can wire a
// sketch.HLL today, but every operation is a stub until SP-03 lands the real implementation.
func NewHLL(registers int) *HLL {
	return &HLL{registers: registers}
}

// Add is a no-op: the stub has no register array to update, and Add has no return value at all to
// signal otherwise.
func (h *HLL) Add(key []byte) {}

// Cardinality always returns 0: with nothing added, the stub's true cardinality is genuinely 0.
func (h *HLL) Cardinality() uint64 { return 0 }

// MergeFrom always reports core.ErrNotImplemented.
func (h *HLL) MergeFrom(o *HLL) error { return core.ErrNotImplemented }

// Header always returns the zero Header (see Bloom.Header's comment for why).
func (h *HLL) Header() Header { return Header{} }

// MarshalBinary always reports core.ErrNotImplemented.
func (h *HLL) MarshalBinary() ([]byte, error) { return nil, core.ErrNotImplemented }

// UnmarshalBinary always reports core.ErrNotImplemented.
func (h *HLL) UnmarshalBinary(data []byte) error { return core.ErrNotImplemented }

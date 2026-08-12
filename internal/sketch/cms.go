package sketch

import "github.com/qompack/qompack/internal/core"

// CMS is the Count-Min sketch behind sketches/touch.cms (00-ARCHITECTURE.md §5.7, Appendix A):
// approximate per-key touch-frequency counting, warm-started at session start (O4).
type CMS struct {
	epsilon float64
	delta   float64
}

// NewCMS returns a CMS sized for error factor epsilon and failure probability delta.
// Constructing always succeeds — no counter table is allocated by the stub, since Add is a
// no-op — so wave-0 composition roots can wire a sketch.CMS today, but every operation is a stub
// until SP-03 lands the real width=⌈e/ε⌉, depth=⌈ln(1/δ)⌉ sizing (00-ARCHITECTURE.md §5.7).
func NewCMS(epsilon, delta float64) *CMS {
	return &CMS{epsilon: epsilon, delta: delta}
}

// Add is a no-op: the stub has no counter table to increment, and Add has no return value at all
// to signal otherwise.
func (c *CMS) Add(key []byte, n uint32) {}

// Estimate always returns 0: with nothing added, 0 is the only true count the stub can honestly
// report — a real CMS's defining guarantee (Estimate never under-counts the true count) still
// holds trivially here, since the stub's own true count is always 0 too.
func (c *CMS) Estimate(key []byte) uint32 { return 0 }

// MergeFrom always reports core.ErrNotImplemented.
func (c *CMS) MergeFrom(o *CMS) error { return core.ErrNotImplemented }

// Scale is a no-op: there are no counters to decay.
func (c *CMS) Scale(factor float64) {}

// HeavyHitters always returns nil: with nothing added, there are no heavy hitters to report.
func (c *CMS) HeavyHitters(mg *MisraGries, n int) []Counted { return nil }

// Header always returns the zero Header (see Bloom.Header's comment for why).
func (c *CMS) Header() Header { return Header{} }

// MarshalBinary always reports core.ErrNotImplemented.
func (c *CMS) MarshalBinary() ([]byte, error) { return nil, core.ErrNotImplemented }

// UnmarshalBinary always reports core.ErrNotImplemented.
func (c *CMS) UnmarshalBinary(data []byte) error { return core.ErrNotImplemented }

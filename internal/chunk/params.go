package chunk

import (
	"fmt"
	"math/bits"

	"github.com/qompack/qompack/internal/config"
)

// GearWindow is the length, in bytes, of the sliding window the rolling hash's boundary decision
// depends on — and therefore the number of bytes nextCut primes its fingerprint over before it
// starts testing.
//
// 64 is not a tuning choice, it is the fingerprint's width. The recurrence fp = (fp<<1) ^ gear[b]
// shifts each byte's contribution one bit further left per byte, so bit j of fp is the XOR of one
// bit from each of the last j+1 gear entries and bit 63 — the highest bit any mask can test —
// depends on exactly 64 bytes. Feed 64 bytes into a zeroed fingerprint and every one of its 64 bits
// already holds the same value it would have held after a megabyte of history.
//
// That is what makes the whole design work. Because Normalized guarantees Min >= GearWindow, the
// priming window always lies inside the current chunk, so nextCut can be handed a bare []byte
// starting at a chunk boundary and reproduce the fingerprint exactly — no carry-in from the
// previous chunk, no cross-chunk state to thread through. SplitStream therefore needs no carry
// buffer at all: it can slide its window to the new chunk start and keep going.
const GearWindow = 64

// targetFloor and targetCeiling bound the normalized Target. Both are powers of two, so clamping
// into this range can never turn a power-of-two Target into a non-power-of-two one — which the
// mask derivation (bits.TrailingZeros64) depends on absolutely.
//
// The floor exists because the masks are derived as log2(Target)±2: below 256 the narrow mask would
// be under 6 bits wide, at which point boundaries fire so often that chunks are all Min-sized and
// the content-defined property is gone. The ceiling exists because SplitStream sizes its buffer at
// 2*Max <= 32*Target, and an unbounded Target would let a config allocate an unbounded buffer on
// the hot path.
const (
	targetFloor   = 256
	targetCeiling = 1 << 20
)

// The Min and Max clamps, as multiples of Target. minTargetRatio keeps the pre-Target scan window
// (Min..Target) wide enough to be worth having; minFallbackRatio is where Min lands when the
// configured value would swallow Target entirely; maxFloorRatio and maxCeilingRatio bound the
// force-cut distance so the chunk-size distribution stays recognizable and the streaming buffer
// stays bounded.
const (
	minTargetRatio   = 8
	minFallbackRatio = 4
	maxFloorRatio    = 2
	maxCeilingRatio  = 16
	maxDefaultRatio  = 4
)

// Validate reports whether p's boundaries are usable: 0 < Min < Target < Max
// (00-ARCHITECTURE.md §5.5; internal/config's own store.chunk.{min,target,max} range tags agree —
// see ChunkCfg in internal/config/config.go). Validate is fully specified by the architecture, so
// SP-01 implements it for real rather than stubbing it (00-ARCHITECTURE.md §14.1): every
// constructor downstream of a loaded config.Config needs a working check before SP-04's real
// Chunker exists.
func (p Params) Validate() error {
	if p.Min <= 0 {
		return fmt.Errorf("chunk: Params.Validate: min must be positive, got %d", p.Min)
	}
	if p.Target <= p.Min {
		return fmt.Errorf("chunk: Params.Validate: target (%d) must be greater than min (%d)", p.Target, p.Min)
	}
	if p.Max <= p.Target {
		return fmt.Errorf("chunk: Params.Validate: max (%d) must be greater than target (%d)", p.Max, p.Target)
	}
	return nil
}

// DefaultParams returns the FastCDC parameters from config.Defaults().Store.Chunk. It is fully
// specified by the architecture, so SP-01 implements it for real rather than stubbing it
// (00-ARCHITECTURE.md §14.1).
//
// §5.5 gives DefaultParams no arguments, so — unlike New(p Params), which the daemon uses to
// construct a Chunker from whatever config.Config a project actually loaded — DefaultParams
// always reads the built-in defaults, never a loaded project config. Callers that already hold a
// config.Config should read its Store.Chunk directly and pass the result to New; DefaultParams
// exists for callers (tests, tools, a bare chunk.New(chunk.DefaultParams())) that do not.
func DefaultParams() Params {
	c := config.Defaults().Store.Chunk
	return Params{Min: c.Min, Target: c.Target, Max: c.Max}
}

// FromConfig returns the Params a loaded project configuration asks for, read verbatim from
// store.chunk.
//
// Verbatim is the point. FromConfig deliberately does NOT normalize: `qompack config`, the config
// validator and any error message about chunk sizes must be able to show the user the numbers their
// own qompack.json actually contains, not silently rewritten ones. Only New — the one place that
// has to run the algorithm — is allowed to move them, and what it moved them to is readable back
// through ParamsReporter.
func FromConfig(c config.Config) Params {
	ch := c.Store.Chunk
	return Params{Min: ch.Min, Target: ch.Target, Max: ch.Max}
}

// Normalized returns p adjusted, deterministically, into the shape FastCDC can actually run with.
//
// Validate and Normalized answer two different questions and neither can do the other's job.
// Validate is the CONFIG gate: 0 < Min < Target < Max, which is exactly what internal/config's own
// range tags accept, so it must never reject a triple a valid qompack.json can express. Normalized
// is the ALGORITHM gate: the boundary scan additionally needs a power-of-two Target (the masks are
// derived from its bit width), a Min at least as large as the priming window, and a Max within a
// sane multiple of Target. Tightening Validate to demand those would reject working configurations;
// letting New run without them would panic or produce nonsense. So New normalizes, and reports what
// it normalized to.
//
// The clamps are applied in this order, and the order matters — Min and Max are both expressed
// relative to Target, so Target has to settle first:
//
//  1. Target. A non-positive Target falls back to the built-in default. Otherwise it rounds DOWN to
//     the nearest power of two (rounding down rather than to-nearest so a configured Target is
//     never silently exceeded), then clamps into [targetFloor, targetCeiling]. Both bounds are
//     powers of two, so the result always is.
//  2. Min. Raised to at least GearWindow (so the priming window fits inside the chunk — see
//     GearWindow) and at least Target/8 (so a tiny Min cannot collapse the size distribution onto
//     the floor). If that still leaves Min at or above Target there is no scan window at all, and
//     the documented recovery is Target/4.
//  3. Max. A non-positive Max defaults to 4*Target. Otherwise it clamps into [2*Target, 16*Target]:
//     below 2*Target the force-cut would fire so often that boundaries stop being content-defined,
//     and above 16*Target the streaming buffer (2*Max) grows without bound.
//
// Normalized is idempotent, and it is the identity on DefaultParams — anything else would re-chunk
// every existing store the first time it ran.
func (p Params) Normalized() Params {
	target := p.Target
	if target <= 0 {
		target = DefaultParams().Target
	}
	// The largest power of two <= target. bits.Len returns the index of the highest set bit plus
	// one, so 1 << (Len-1) is that bit alone.
	target = 1 << (bits.Len(uint(target)) - 1)
	target = clamp(target, targetFloor, targetCeiling)

	minLen := max(p.Min, GearWindow, target/minTargetRatio)
	if minLen >= target {
		minLen = target / minFallbackRatio
	}

	maxLen := p.Max
	if maxLen <= 0 {
		maxLen = maxDefaultRatio * target
	}
	maxLen = clamp(maxLen, maxFloorRatio*target, maxCeilingRatio*target)

	return Params{Min: minLen, Target: target, Max: maxLen}
}

// clamp returns v confined to [lo, hi].
func clamp(v, lo, hi int) int {
	return min(max(v, lo), hi)
}

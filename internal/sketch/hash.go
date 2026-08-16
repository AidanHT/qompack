package sketch

import (
	"encoding/binary"

	"github.com/qompack/qompack/internal/core"
)

// The hash domains this package mints, in the sense of internal/core's domain-separation registry
// (§4): every digest is sha256(domain || 0x00 || key), so a key hashed for the Bloom filter can
// never collide with the same key hashed for the Count-Min sketch. Their errors would otherwise
// correlate — a path that false-positived in one sketch would tend to false-positive in the other,
// and the two signals the scheduler treats as independent evidence would not be.
//
// These strings are a wire format, not identifiers. Changing one re-keys every sketch already on
// disk: the bits are set at the old indices and read at the new ones, so the filter silently
// reports "never tried" for everything it has ever seen. That is a FormatVersion bump, not a
// rename.
const (
	// domainBloom is the negative-knowledge membership domain (sketches/tried.bloom).
	domainBloom = "qompack.sketch.bloom.v1"
	// domainCMS is the touch-frequency domain (sketches/touch.cms).
	domainCMS = "qompack.sketch.cms.v1"
	// domainHLL is the exploration-breadth domain (sketches/explore.hll).
	domainHLL = "qompack.sketch.hll.v1"
)

// hash128 returns two 64-bit values derived from one domain-separated SHA-256. A single hash call
// supplies both words Kirsch-Mitzenmacher double hashing needs, which is why a k=7 Bloom costs one
// SHA-256 rather than seven hashes.
//
// Its two consumers use different members of that family, and the difference matters to what the
// odd-forcing below is for:
//
//   - cms.go's cellIndex uses the PLAIN form, g_j(x) = h1 + j·h2, one probe per row.
//   - bloom.go's Add and Test use the ENHANCED form, g_i(x) = h1 + i·h2 + i², which adds the
//     quadratic term because without it two keys sharing an h2 keep their probe positions in
//     lockstep and the measured false-positive rate drifts above the analytic one.
//
// h2 is forced odd so the stride is never zero, and the failure that prevents is the Count-Min one.
// Under the plain form a zero stride puts every row's probe at the SAME column, so a depth-5 table
// answers with one row's counter five times over: Estimate stops being a minimum over independent
// rows, which is the property the ε·N-for-1−δ-of-keys bound rests on, and the sketch over-counts
// with nothing left to detect it. Under the enhanced form the i² term separates the probes on its
// own, so a zero stride there would still land on k distinct positions — the odd-forcing is not
// what saves the Bloom filter, and a comment claiming it collapses "all k probes onto one bit" was
// describing the plain form while naming the enhanced one. It applies to both because the pair is
// shared, and it costs one OR.
//
// Keys are opaque bytes. Callers supply already-normalized keys — SP-09 passes
// negknow.Descriptor.Key(), SP-08 passes []byte(paths.Key(path)) — and this package performs no
// normalization of its own, so that a change to path normalization is visible at exactly one site.
//
// hash128 is pure and safe for concurrent use; store (SP-06) calls into this file from its worker
// pool.
func hash128(domain string, key []byte) (h1, h2 uint64) {
	sum := core.HashBytes(domain, key)
	h1 = binary.LittleEndian.Uint64(sum[0:8])
	h2 = binary.LittleEndian.Uint64(sum[8:16]) | 1
	return h1, h2
}

// splitmix64 is the fixed, fully-specified mixer used to derive MinHash permutation coefficients.
// Its constants are frozen: changing them changes every stored Signature, so every near-duplicate
// decision the store has already recorded would have been made under a different function.
// It is pure and safe for concurrent use.
func splitmix64(x uint64) uint64 {
	x += 0x9E3779B97F4A7C15
	x = (x ^ (x >> 30)) * 0xBF58476D1CE4E5B9
	x = (x ^ (x >> 27)) * 0x94D049BB133111EB
	return x ^ (x >> 31)
}

// The FNV-1a 64-bit parameters, spelled out rather than imported so that this file is the single
// place a reader has to look to reproduce a signature by hand.
const (
	// fnvOffset64 is the FNV-1a 64-bit offset basis.
	fnvOffset64 = 14695981039346656037
	// fnvPrime64 is the FNV-1a 64-bit prime.
	fnvPrime64 = 1099511628211
)

// fnv1a64 is the shingle hash for MinHash: stable forever, and roughly 5 ns for an 8-byte shingle.
// A cryptographic hash is unnecessary here because the value never leaves the signature and is
// never used as an identifier — but the function must never change, because signatures are
// persisted in an append-only index and a changed shingle hash makes every stored signature
// incomparable with every new one. It is pure and safe for concurrent use.
func fnv1a64(b []byte) uint64 {
	h := uint64(fnvOffset64)
	for _, c := range b {
		h ^= uint64(c)
		h *= fnvPrime64
	}
	return h
}

// Package tokens provides a baseline token estimator (G10.2 groundwork): content classification
// (Classify) and a byte/dimension/page-count-driven token estimate (Estimator), with a
// per-project calibration factor that nudges the estimate toward the host's own accounting over
// time.
//
// This is deliberately a BASELINE: images and PDFs are estimated from their actual dimensions or
// page count rather than a flat guess, and every chunk-level estimate in EstimateRoot is memoized
// by content hash so repeated roots over the same chunks cost nothing extra, but the per-byte
// formulas themselves are heuristics. SP-06 replaces EstimateRoot's per-chunk value with a
// measured one, keyed by the same map — the memoization shape here is what SP-06 inherits.
//
// tokens is foundation-layer (§3.2): it imports internal/core, internal/paths and internal/config
// only. EstimateRoot takes []core.ChunkRef rather than a store.Root specifically so that this
// package never needs to import internal/store (§3.2, §5.20).
package tokens

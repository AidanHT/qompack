// Package sketch implements the permanent-memory probabilistic sketches of 00-ARCHITECTURE.md
// §5.7: the Bloom filter behind tried.bloom (Appendix A), the Count-Min sketch behind touch.cms,
// the HyperLogLog behind explore.hll, the Misra-Gries top-k counter, and MinHash near-duplicate
// signatures over canonicalized content.
//
// sketch is foundation-only (00-ARCHITECTURE.md §3.2: sketch may import core, paths, config,
// logging, obs and nothing else); concretely it imports only core and the standard library below.
//
// SP-01 ships the complete §5.7 type set as real declarations and every operation as a stub:
// Save/Load and every Marshal/UnmarshalBinary report core.ErrNotImplemented; every accessor with
// no error return reports the documented zero value (Bloom.Test's false, in particular, is load-
// bearing — see its own doc comment) — until SP-03 lands the real sketch math.
//
// §13 invariant 3 governs this whole package: a bloom filter (and, by the same logic, every other
// sketch here) is a cache, never the source of truth. Every membership or estimate answer these
// stubs give is the safe, empty-state answer — never a fabricated positive — so nothing downstream
// can mistake stub output for a real signal.
package sketch

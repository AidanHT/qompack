// Package chunk implements FastCDC content-defined chunking, the L1 store's boundary-detection
// primitive (00-ARCHITECTURE.md §5.5): splitting a byte stream into content-addressed chunks whose
// boundaries stay stable under small edits, so an edited file dedups against its own prior
// versions instead of rehashing whole-file.
//
// # Why content-defined
//
// Fixed-size blocking would be simpler, faster and smaller. It is rejected because inserting one
// byte at the front of a file shifts every subsequent block and destroys all deduplication against
// that file's own previous version — which, for a store whose whole job is holding many nearly
// identical snapshots of the same tool output, is the only case that matters. A content-defined
// boundary is a function of the bytes around it rather than of a position, so a local edit
// perturbs a bounded number of chunks and the rest of the stream keeps its identity.
//
// # How
//
// A 64-bit rolling fingerprint is updated per byte as fp = (fp<<1) ^ gear[b], where gear is a
// frozen 256-entry table generated from a single seed (gear.go). A boundary is declared wherever
// fp has zeros in a mask of high bits. The recurrence is XOR-and-shift rather than
// multiply-and-add on purpose: with no carry propagation, bit j of fp is exactly the XOR of one bit
// from each of the last j+1 gear entries, so a mask made only of high bits makes the boundary
// decision at any position a pure function of the preceding 64 bytes — a property of the content,
// never of where the current chunk happens to have started. That is what lets the scan be primed
// over a 64-byte window instead of rolled from the chunk start, and what lets SplitStream run with
// no cross-chunk carry state at all.
//
// Two masks are used, not one (FastCDC normalized chunking, Xia et al. 2016): a strict one below
// Target and a lenient one above it, which squeezes the chunk-length distribution toward Target
// from both sides. The normalization level is NC=1 rather than the paper's NC=2 — a deliberate
// trade of size-distribution tightness for boundary stability; see maskWidthDelta in fastcdc.go for
// the measurements behind that choice. Below Min nothing is tested and at Max the chunk is
// force-cut, which bounds chunk size in both directions.
//
// # What is frozen
//
// The gear seed, the mask derivation, the Min/Target/Max normalization and the two domain strings
// are all wire format, not tunables. Changing any of them moves every boundary, which re-keys every
// chunk in the CAS and invalidates every root in index/roots.jsonl, with no migration short of
// re-ingesting every session. TestGearTableGolden and TestSplit_GoldenBoundaries exist to make that
// impossible to do by accident.
//
// # Scope
//
// chunk is foundation-only (00-ARCHITECTURE.md §3.2: chunk may import core, paths, config, logging,
// obs and nothing else); concretely it imports only core and config, plus the standard library.
package chunk

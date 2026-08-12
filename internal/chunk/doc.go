// Package chunk implements FastCDC content-defined chunking, the L1 store's boundary-detection
// primitive (00-ARCHITECTURE.md §5.5): splitting a byte stream into content-addressed chunks whose
// boundaries stay stable under small edits, so an edited file dedups against its own prior
// versions instead of rehashing whole-file.
//
// chunk is foundation-only (00-ARCHITECTURE.md §3.2: chunk may import core, paths, config,
// logging, obs and nothing else); concretely it imports only core and config below.
//
// SP-01 ships the complete §5.5 type set as real declarations. Params.Validate, DefaultParams and
// RootHash are fully specified by the architecture and so are implemented for real
// (00-ARCHITECTURE.md §14.1 of plans/V1-SP-01-foundation-toolchain-and-contracts.md); every
// Chunker operation is a stub — Split returns the documented nil slice (it has no error return),
// SplitStream returns core.ErrNotImplemented — until SP-04 lands the real FastCDC implementation.
package chunk

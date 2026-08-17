package chunk

import "io"

// Chunker splits byte streams into content-defined Chunks (00-ARCHITECTURE.md §5.5).
//
// Implementations returned by New are immutable and safe for concurrent use: both methods derive
// everything they need from their arguments and the frozen gear table, and neither writes to the
// Chunker.
type Chunker interface {
	// Split returns the chunks of data, in order. It is allocation-free apart from the returned
	// slice and a single reused hasher, and safe for concurrent use.
	Split(data []byte) []Chunk
	// SplitStream streams the same boundaries Split would find, reading from r and calling fn once
	// per chunk with the chunk's metadata and its bytes. The bytes alias an internal buffer and are
	// valid only for the duration of the call.
	SplitStream(r io.Reader, fn func(Chunk, []byte) error) error
}

// ParamsReporter is implemented by Chunkers that can report the parameters they are actually
// running with. New's Chunker implements it.
//
// It is a separate, additive interface rather than a third method on Chunker because it answers a
// different kind of question. Chunker is the seam every consumer codes against and every fake in
// the tree has to satisfy (see chunktest.RunChunkerSuite); adding a method to it would break every
// such implementation for the benefit of the handful of callers — `qompack doctor`, the store's
// index sizing, this package's own tests — that need to know what New made of their config after
// Params.Normalized clamped it. Those callers type-assert:
//
//	if reporter, ok := c.(chunk.ParamsReporter); ok {
//		effective := reporter.Params()
//	}
type ParamsReporter interface {
	// Params returns the effective, post-normalization parameters. They may differ from the Params
	// New was called with; see Params.Normalized for exactly how and why.
	Params() Params
}

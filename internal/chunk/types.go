package chunk

import "github.com/qompack/qompack/internal/core"

// Params holds the FastCDC boundary parameters (00-ARCHITECTURE.md §5.5, Qompack.md §8.1): chunk
// sizes stay within [Min, Max], gravitating toward Target. The zero value is not valid — use
// DefaultParams, or supply values that satisfy Validate, before calling New.
type Params struct {
	// Min is the minimum chunk size, in bytes.
	Min int
	// Target is the target chunk size, in bytes, that FastCDC's rolling hash gravitates toward.
	Target int
	// Max is the maximum chunk size, in bytes: a chunk is force-cut here even when no
	// content-defined boundary was found first.
	Max int
}

// Chunk is one content-defined chunk: its position and length in the original stream, and the
// domain-separated hash of its bytes (core.DomainChunk).
type Chunk struct {
	// Offset is the chunk's byte offset in the original stream.
	Offset int64
	// Len is the chunk's length in bytes.
	Len int
	// Hash is core.HashBytes(core.DomainChunk, <chunk bytes>).
	Hash core.Hash
}

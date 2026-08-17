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

// Ref returns the core.ChunkRef naming this chunk: its content hash and its length.
//
// Offset is deliberately dropped. A ChunkRef is what goes into index/roots.jsonl, and a root is an
// ORDERED list of refs — the offset of the n-th chunk is the sum of the lengths before it, so
// storing it as well would be a second, independently corruptible copy of the same fact. Length
// does survive, because store needs it to size a read without first fetching the chunk.
func (c Chunk) Ref() core.ChunkRef {
	return core.ChunkRef{Hash: c.Hash, Len: c.Len}
}

// Refs projects a chunk list onto the core.ChunkRef list a root record is made of, preserving
// order. It returns nil for an empty input, matching Split's own "no chunks means nil" convention
// so that a root over nothing serializes as a JSON null rather than an empty array.
func Refs(chunks []Chunk) []core.ChunkRef {
	if len(chunks) == 0 {
		return nil
	}
	refs := make([]core.ChunkRef, len(chunks))
	for i, c := range chunks {
		refs[i] = c.Ref()
	}
	return refs
}

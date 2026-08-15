package chunk

import (
	"io"

	"github.com/qompack/qompack/internal/core"
)

// SplitStream is not implemented in this commit.
//
// Split and RootHash land first because SplitStream is defined in terms of them — it must produce
// exactly the chunk sequence Split produces over the same bytes, for every possible pattern of Read
// sizes, and that contract cannot be asserted against a Split that does not exist yet. The next
// commit replaces this body and brings the boundary-stability properties, the fuzz targets and the
// benchmarks with it.
//
// It returns core.ErrNotImplemented rather than panicking so the conformance suite's shape block —
// which accepts nil or any of the four known sentinels — keeps passing, and so chunktest's
// skipIfStub probe keeps reporting this package as unfinished.
func (c *chunker) SplitStream(r io.Reader, fn func(Chunk, []byte) error) error {
	return core.ErrNotImplemented
}

package chunk_test

import (
	"testing"

	"github.com/qompack/qompack/internal/chunk"
	"github.com/qompack/qompack/internal/chunk/chunktest"
)

// TestChunkConformance runs SP-01's Chunker conformance suite against SP-04's real chunker and
// requires it to pass with ZERO skips.
//
// The suite is the cross-implementation contract SP-06 will hold its own store-side chunker to, so
// running it here is what makes the contract real rather than aspirational: until a factory exists
// that is not a stub, skipIfStub swallows the whole behaviour block and the boundary-stability and
// determinism assertions SP-01 wrote never execute.
//
// DefaultParams rather than a tuned set, because the conformance contract is about the chunker's
// behaviour at the parameters Appendix C actually ships.
func TestChunkConformance(t *testing.T) {
	chunktest.RunChunkerSuite(t, "chunk.New", func(t *testing.T) chunk.Chunker {
		return chunk.New(chunk.DefaultParams())
	})
}

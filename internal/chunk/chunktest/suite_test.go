package chunktest_test

import (
	"io"
	"testing"

	"github.com/qompack/qompack/internal/chunk"
	"github.com/qompack/qompack/internal/chunk/chunktest"
	"github.com/qompack/qompack/internal/core"
)

// fakeStubChunker mirrors the shape of an SP-01-style stub Chunker: Split returns nil and
// SplitStream reports core.ErrNotImplemented, exactly like chunk.New's own stub does today. It
// exists only to exercise RunChunkerSuite before SP-04 ships a real Chunker.
type fakeStubChunker struct{}

func (fakeStubChunker) Split(data []byte) []chunk.Chunk { return nil }

func (fakeStubChunker) SplitStream(r io.Reader, fn func(chunk.Chunk, []byte) error) error {
	return core.ErrNotImplemented
}

// TestRunChunkerSuite_StubIsSkipped proves the suite's shape block passes against a stub Chunker
// and that its behaviour block is skipped with the exact Rule W-1 message. SP-04 reuses
// RunChunkerSuite unchanged, pointed at its real implementation, to flip that skip off.
func TestRunChunkerSuite_StubIsSkipped(t *testing.T) {
	chunktest.RunChunkerSuite(t, "fake-stub", func(t *testing.T) chunk.Chunker {
		return fakeStubChunker{}
	})
}

// TestRunChunkerSuite_AgainstQompackStub exercises RunChunkerSuite against the real chunk.New
// stub, end to end, so a change to chunk.New's stub behaviour that breaks the conformance suite
// is caught here rather than only once SP-04 lands.
func TestRunChunkerSuite_AgainstQompackStub(t *testing.T) {
	chunktest.RunChunkerSuite(t, "chunk.New-stub", func(t *testing.T) chunk.Chunker {
		return chunk.New(chunk.DefaultParams())
	})
}

package chunk_test

import (
	"testing"

	"github.com/qompack/qompack/internal/chunk"
	"github.com/qompack/qompack/internal/core"
	"github.com/stretchr/testify/require"
)

// TestRootHash_Formula pins chunk.RootHash to 00-ARCHITECTURE.md §5.5's closed-form definition:
// the RootHash of two chunks must equal core.HashBytes(core.DomainRoot, h1||h2). Unlike every
// other method in this package, RootHash is real (not a stub), so this test runs unconditionally
// — it is never gated behind a Rule W-1 skip.
func TestRootHash_Formula(t *testing.T) {
	h1 := core.HashBytes(core.DomainChunk, []byte("chunk one"))
	h2 := core.HashBytes(core.DomainChunk, []byte("chunk two"))
	chunks := []chunk.Chunk{
		{Offset: 0, Len: 9, Hash: h1},
		{Offset: 9, Len: 9, Hash: h2},
	}

	var concat []byte
	concat = append(concat, h1[:]...)
	concat = append(concat, h2[:]...)
	want := core.HashBytes(core.DomainRoot, concat)

	require.Equal(t, want, chunk.RootHash(chunks))
}

// TestRootHash_Empty asserts the degenerate zero-chunk case still follows the same formula rather
// than special-casing to the zero Hash.
func TestRootHash_Empty(t *testing.T) {
	require.Equal(t, core.HashBytes(core.DomainRoot, nil), chunk.RootHash(nil))
}

// TestRootHash_OrderSensitive asserts the two input chunk hashes are concatenated in argument
// order, not sorted or otherwise normalized: swapping their order must change the root.
func TestRootHash_OrderSensitive(t *testing.T) {
	h1 := core.HashBytes(core.DomainChunk, []byte("alpha"))
	h2 := core.HashBytes(core.DomainChunk, []byte("beta"))
	forward := []chunk.Chunk{{Hash: h1}, {Hash: h2}}
	reverse := []chunk.Chunk{{Hash: h2}, {Hash: h1}}
	require.NotEqual(t, chunk.RootHash(forward), chunk.RootHash(reverse))
}

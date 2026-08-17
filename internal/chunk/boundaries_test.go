package chunk_test

import (
	"encoding/json"
	"testing"

	"github.com/qompack/qompack/internal/chunk"
	"github.com/qompack/qompack/internal/testutil"
	"github.com/stretchr/testify/require"
)

// TestSplit_GoldenBoundaries freezes the actual boundary offsets for a 1 MiB fixture. This is the
// test that turns "the gear table and the mask derivation are frozen" from a comment into a fact:
// any change to gearSeed, to the rolling-hash recurrence, to the mask widths, or to the Min/Target/
// Max clamps moves these offsets, and moving them re-chunks every object in every existing store.
func TestSplit_GoldenBoundaries(t *testing.T) {
	t.Parallel()
	c := chunk.New(defaults())
	data := pseudoRandomBytes(7, 1<<20)

	chunks := c.Split(data)
	require.NotEmpty(t, chunks)

	offsets := make([]int64, 0, len(chunks))
	for _, ch := range chunks {
		offsets = append(offsets, ch.Offset)
	}
	b, err := json.MarshalIndent(offsets, "", "  ")
	require.NoError(t, err)
	testutil.Golden(t, "boundaries-1mib.json", append(b, '\n'))
}

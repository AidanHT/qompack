package chunk_test

import (
	"bytes"
	"testing"

	"github.com/qompack/qompack/internal/chunk"
	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"
)

// The two properties below are stated over generated inputs rather than fixtures because they are
// the invariants every downstream layer silently assumes. store reassembles an object by
// concatenating its chunks in root order, and tokens sizes a root by summing chunk lengths; both
// are wrong the instant a chunk is empty, overlapping, or missing, and neither would notice.
//
// The inputs are generated from a drawn seed and a drawn alphabet size rather than as a drawn
// []byte: rapid's SliceOfN would need 200 000 draws to build one interesting input, whereas a seed
// plus an alphabet size covers the whole spectrum from a constant run (every chunk force-cut at
// Max) to incompressible noise in two draws, and still shrinks to a minimal counterexample.

// propInputMaxLen bounds generated inputs at a size that still spans several chunks under the
// default Params while keeping a full rapid run inside the package's test budget.
const propInputMaxLen = 120000

// drawInput draws a length, a seed and an alphabet size, and returns the corresponding bytes.
func drawInput(rt *rapid.T) []byte {
	n := rapid.IntRange(0, propInputMaxLen).Draw(rt, "len")
	seed := rapid.Uint64().Draw(rt, "seed")
	alphabet := rapid.IntRange(1, 256).Draw(rt, "alphabet")
	return patternedBytes(seed, n, alphabet)
}

// TestPropSizeBounds asserts the §5.5 size contract holds for arbitrary content: every chunk but
// the last lies in [Min, Max], and the last is non-empty and no longer than Max. The Max half is
// the one that actually needs a property test — it is only ever exercised by content with no
// content-defined boundary in 16 KB, which a hand-written fixture reaches only by accident.
func TestPropSizeBounds(t *testing.T) {
	t.Parallel()
	p := chunk.DefaultParams()
	c := chunk.New(p)

	rapid.Check(t, func(rt *rapid.T) {
		data := drawInput(rt)
		chunks := c.Split(data)
		if len(data) == 0 {
			require.Nil(rt, chunks)
			return
		}
		require.NotEmpty(rt, chunks)

		for i, ch := range chunks {
			require.Positive(rt, ch.Len, "chunk %d is empty", i)
			require.LessOrEqual(rt, ch.Len, p.Max, "chunk %d exceeds Max", i)
			if i < len(chunks)-1 {
				require.GreaterOrEqual(rt, ch.Len, p.Min, "chunk %d is below Min", i)
			}
		}
	})
}

// TestPropAllBytesCovered asserts the partition property: the chunks of any input are contiguous,
// start at 0, and concatenate back to exactly that input. It is the one property whose violation
// would corrupt stored content rather than merely degrading deduplication.
func TestPropAllBytesCovered(t *testing.T) {
	t.Parallel()
	c := chunk.New(chunk.DefaultParams())

	rapid.Check(t, func(rt *rapid.T) {
		data := drawInput(rt)
		chunks := c.Split(data)

		var (
			off      int64
			rebuilt  []byte
			totalLen int
		)
		for i, ch := range chunks {
			require.Equal(rt, off, ch.Offset, "chunk %d does not start where chunk %d ended", i, i-1)
			require.LessOrEqual(rt, int(ch.Offset)+ch.Len, len(data), "chunk %d runs past the input", i)
			rebuilt = append(rebuilt, data[ch.Offset:int(ch.Offset)+ch.Len]...)
			off += int64(ch.Len)
			totalLen += ch.Len
		}
		require.Equal(rt, len(data), totalLen, "chunk lengths do not sum to the input length")
		require.True(rt, bytes.Equal(data, rebuilt), "chunks do not reassemble the input")
	})
}

// TestPropStreamMatchesSplit asserts SplitStream's equivalence to Split over generated inputs and
// generated reader granularities, which is the pair of axes a fixed table of sizes cannot cover:
// the interesting cases are the ones where a Read boundary happens to land exactly on a chunk
// boundary, on Max, or one byte either side of them.
func TestPropStreamMatchesSplit(t *testing.T) {
	t.Parallel()
	c := chunk.New(chunk.DefaultParams())

	rapid.Check(t, func(rt *rapid.T) {
		data := drawInput(rt)
		readSize := rapid.IntRange(1, 40000).Draw(rt, "readSize")

		var got []chunk.Chunk
		err := c.SplitStream(&chunkedReader{data: data, size: readSize},
			func(ch chunk.Chunk, _ []byte) error { got = append(got, ch); return nil })
		require.NoError(rt, err)
		require.Equal(rt, c.Split(data), got)
	})
}

// TestPropPrefixDetermines asserts the property that makes SplitStream possible at all: a chunk's
// boundary depends only on the bytes from the chunk's start up to Max past it, never on anything
// later in the stream. Concretely, appending arbitrary bytes to an input must leave every chunk
// except the final one untouched.
//
// Without this, streaming and batch chunking could not agree, and no incremental ingest would be
// possible — every append to a log file would re-chunk the whole file.
func TestPropPrefixDetermines(t *testing.T) {
	t.Parallel()
	c := chunk.New(chunk.DefaultParams())

	rapid.Check(t, func(rt *rapid.T) {
		head := drawInput(rt)
		tail := patternedBytes(rapid.Uint64().Draw(rt, "tailSeed"),
			rapid.IntRange(0, 20000).Draw(rt, "tailLen"),
			rapid.IntRange(1, 256).Draw(rt, "tailAlphabet"))

		before := c.Split(head)
		after := c.Split(append(append([]byte(nil), head...), tail...))

		if len(before) == 0 {
			return
		}
		// Every chunk of `before` except the last was decided without seeing the tail.
		for i := 0; i < len(before)-1; i++ {
			require.Less(rt, i, len(after), "appending bytes deleted chunk %d", i)
			require.Equal(rt, before[i], after[i], "appending bytes changed chunk %d", i)
		}
	})
}

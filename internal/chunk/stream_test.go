package chunk_test

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"testing"

	"github.com/qompack/qompack/internal/chunk"
	"github.com/stretchr/testify/require"
)

// TestSplitStream_MatchesSplit is the central contract of SplitStream: for the same bytes it must
// produce exactly the chunk sequence Split produces, no matter how the reader chose to deliver
// them. That is not a convenience — store's ingest path uses SplitStream and its verifier uses
// Split, so any divergence would make a re-read of a stored object fail to reproduce its own root.
//
// The reader sizes span the three regimes the refill loop has to survive: one byte per Read (the
// buffer is refilled hundreds of thousands of times and the "did we make progress" logic is under
// maximum pressure), a small odd size that never aligns with any internal boundary, a size equal to
// a config default, and one Read for the whole input.
func TestSplitStream_MatchesSplit(t *testing.T) {
	t.Parallel()

	sizes := []int{0, 1, 1023, 1024, 16385, 100000, 4 << 20}
	readSizes := []int{1, 7, 4096, 0} // 0 means "everything in one Read"

	c := chunk.New(chunk.DefaultParams())
	for _, size := range sizes {
		data := pseudoRandomBytes(13, size)
		want := c.Split(data)

		for _, readSize := range readSizes {
			// One byte at a time over 4 MiB is four million Read calls; it adds nothing over the
			// same path already exercised at 100 KB and would dominate the package's test time
			// (and its -race time by an order of magnitude more).
			if size > 1<<20 && readSize > 0 && readSize < 4096 {
				continue
			}
			t.Run(fmt.Sprintf("size=%d/read=%d", size, readSize), func(t *testing.T) {
				got, payloads := collectStream(t, c, data, readSize)
				require.Equal(t, want, got, "streamed chunk metadata diverged from Split")
				require.Len(t, payloads, len(want))
				for i, p := range payloads {
					require.True(t, bytes.Equal(data[want[i].Offset:int(want[i].Offset)+want[i].Len], p),
						"chunk %d payload diverged from the input slice", i)
				}
			})
		}
	}
}

// TestSplitStream_NilReader pins the guard contract. test/guards' TestAllStubsReturnNotImplemented
// walks every seam reflectively and calls each error-returning method with ZERO-VALUED arguments,
// which for SplitStream means a nil io.Reader and a nil callback. It requires no panic and an error
// that is either nil or wraps core.ErrNotImplemented, so a nil reader must be answered with a
// silent nil — there is nothing to read and nothing to report.
func TestSplitStream_NilReader(t *testing.T) {
	t.Parallel()
	c := chunk.New(chunk.DefaultParams())
	require.NotPanics(t, func() {
		require.NoError(t, c.SplitStream(nil, nil))
	})
	require.NotPanics(t, func() {
		require.NoError(t, c.SplitStream(nil, func(chunk.Chunk, []byte) error { return nil }))
	})
}

// TestSplitStream_NilCallbackWithRealReader asserts the other half of that rule: a real reader with
// no callback is a programming error, not a no-op, and must be reported rather than silently
// consuming (and discarding) the caller's stream.
func TestSplitStream_NilCallbackWithRealReader(t *testing.T) {
	t.Parallel()
	c := chunk.New(chunk.DefaultParams())
	err := c.SplitStream(bytes.NewReader([]byte("hello")), nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "fn")
}

// TestSplitStream_CallbackError asserts a callback failure aborts immediately and is returned
// unwrapped. Unwrapped matters: store's ingest callback returns errors that the daemon classifies
// by sentinel (core.ErrBudget, a *fs.PathError, …), and wrapping them here would break that
// classification for every caller.
func TestSplitStream_CallbackError(t *testing.T) {
	t.Parallel()
	c := chunk.New(chunk.DefaultParams())
	data := pseudoRandomBytes(14, 200000)
	sentinel := errors.New("callback said no")

	for _, stopAt := range []int{0, 1, 3} {
		t.Run(fmt.Sprintf("stop_at_%d", stopAt), func(t *testing.T) {
			var seen int
			err := c.SplitStream(bytes.NewReader(data), func(chunk.Chunk, []byte) error {
				if seen == stopAt {
					seen++
					return sentinel
				}
				seen++
				return nil
			})
			require.Equal(t, sentinel, err, "the callback's error must be returned unwrapped")
			require.Equal(t, stopAt+1, seen, "SplitStream must stop at the failing chunk, not continue")
		})
	}
}

// TestSplitStream_ReadError asserts a read failure that is not EOF is returned unwrapped and stops
// the walk, and that chunks already delivered before the failure stay delivered — a partial stream
// is a partial stream, not a rollback.
func TestSplitStream_ReadError(t *testing.T) {
	t.Parallel()
	c := chunk.New(chunk.DefaultParams())
	boom := errors.New("disk fell over")

	t.Run("error_after_data", func(t *testing.T) {
		r := &errReader{data: pseudoRandomBytes(15, 200000), err: boom}
		var seen int
		err := c.SplitStream(r, func(chunk.Chunk, []byte) error { seen++; return nil })
		require.Equal(t, boom, err, "a non-EOF read error must be returned unwrapped")
		require.Positive(t, seen, "chunks emitted before the failure must still have been delivered")
	})

	t.Run("error_immediately", func(t *testing.T) {
		r := &errReader{err: boom}
		var seen int
		err := c.SplitStream(r, func(chunk.Chunk, []byte) error { seen++; return nil })
		require.Equal(t, boom, err)
		require.Zero(t, seen)
	})

	t.Run("unexpected_eof_with_no_data_is_an_error", func(t *testing.T) {
		r := &errReader{err: io.ErrUnexpectedEOF}
		err := c.SplitStream(r, func(chunk.Chunk, []byte) error { return nil })
		require.Equal(t, io.ErrUnexpectedEOF, err,
			"ErrUnexpectedEOF with nothing read is a truncated stream, not an empty one")
	})

	t.Run("unexpected_eof_after_data_is_end_of_stream", func(t *testing.T) {
		data := pseudoRandomBytes(16, 50000)
		r := &errReader{data: data, err: io.ErrUnexpectedEOF}
		var got []chunk.Chunk
		err := c.SplitStream(r, func(ch chunk.Chunk, _ []byte) error { got = append(got, ch); return nil })
		require.NoError(t, err, "ErrUnexpectedEOF reported alongside real bytes means the stream ended")
		require.Equal(t, c.Split(data), got)
	})
}

// TestSplitStream_NoProgressReader asserts a reader that keeps returning (0, nil) is reported
// rather than spun on forever. §12.3 budgets the hot path in milliseconds; an unbounded loop inside
// a hook is the one failure mode that costs the user their turn outright.
func TestSplitStream_NoProgressReader(t *testing.T) {
	t.Parallel()
	c := chunk.New(chunk.DefaultParams())
	err := c.SplitStream(stubbornReader{}, func(chunk.Chunk, []byte) error { return nil })
	require.ErrorIs(t, err, io.ErrNoProgress)
}

// stubbornReader always reports "I read nothing, and nothing went wrong", which io.Reader
// discourages but does not forbid.
type stubbornReader struct{}

// Read always returns (0, nil).
func (stubbornReader) Read([]byte) (int, error) { return 0, nil }

// TestSplitStream_BufferAliasingDocumented proves the documented lifetime of the []byte handed to
// the callback: it aliases SplitStream's internal buffer and is valid only for the duration of the
// call. A caller that copies inside the callback sees correct bytes; a caller that retains the
// slice sees whatever the buffer holds later.
//
// The second half is asserted, not merely warned about, because "you must copy" is only a real
// contract if violating it is observably wrong — a test that let a retained slice pass would let a
// future refactor quietly turn the aliasing into a per-chunk copy and nobody would notice the
// allocation regression.
func TestSplitStream_BufferAliasingDocumented(t *testing.T) {
	t.Parallel()
	c := chunk.New(chunk.DefaultParams())
	data := pseudoRandomBytes(17, 1<<20)
	want := c.Split(data)

	var (
		copies   [][]byte
		retained [][]byte
	)
	err := c.SplitStream(bytes.NewReader(data), func(_ chunk.Chunk, payload []byte) error {
		copies = append(copies, append([]byte(nil), payload...))
		retained = append(retained, payload)
		return nil
	})
	require.NoError(t, err)
	require.Len(t, copies, len(want))

	// Copying inside the callback is correct.
	for i, ch := range want {
		require.True(t, bytes.Equal(data[ch.Offset:int(ch.Offset)+ch.Len], copies[i]),
			"chunk %d: the payload seen inside the callback was wrong", i)
	}

	// Retaining the slice is not: at least one retained view must have been overwritten by the
	// time the walk finished. (Not all of them — the tail of the buffer survives.)
	var stale int
	for i := range retained {
		if !bytes.Equal(retained[i], copies[i]) {
			stale++
		}
	}
	require.Positive(t, stale,
		"no retained payload was overwritten: SplitStream is copying per chunk instead of aliasing its buffer")
}

// TestSplitStream_Params asserts the streaming Chunker reports the same effective Params the
// batch one does — they are the same object, and a caller that read Params back to size its own
// buffers would be badly served by two different answers.
func TestSplitStream_Params(t *testing.T) {
	t.Parallel()
	c := chunk.New(chunk.Params{Min: 0, Target: 5000, Max: 0})

	reporter, ok := c.(chunk.ParamsReporter)
	require.True(t, ok, "chunk.New's Chunker must implement ParamsReporter")
	p := reporter.Params()
	require.Equal(t, chunk.Params{Min: 512, Target: 4096, Max: 16384}, p)

	// And the reported Params must be the ones the stream actually used.
	data := pseudoRandomBytes(18, 300000)
	got, _ := collectStream(t, c, data, 4096)
	require.NotEmpty(t, got)
	for i, ch := range got[:len(got)-1] {
		require.GreaterOrEqual(t, ch.Len, p.Min, "chunk %d", i)
		require.LessOrEqual(t, ch.Len, p.Max, "chunk %d", i)
	}
}

// TestSplitStream_EmptyReader asserts an empty stream produces no chunks and no error, matching
// Split(nil) rather than emitting a zero-length chunk.
func TestSplitStream_EmptyReader(t *testing.T) {
	t.Parallel()
	c := chunk.New(chunk.DefaultParams())
	var seen int
	require.NoError(t, c.SplitStream(bytes.NewReader(nil), func(chunk.Chunk, []byte) error { seen++; return nil }))
	require.Zero(t, seen)
}

// TestSplitStream_ExactlyMaxBoundary pins the off-by-one the refill loop is written around. The
// refill condition is `len(buf) > Max`, strictly greater: at exactly Max buffered bytes with more
// input still pending, nextCut cannot yet know whether the force-cut at Max is real or whether a
// content boundary sits at Max+1, so the loop must go back and read more. Inputs at and around
// exact multiples of Max are where a `>=` would either emit a wrong chunk or spin.
func TestSplitStream_ExactlyMaxBoundary(t *testing.T) {
	t.Parallel()
	p := chunk.DefaultParams()
	c := chunk.New(p)

	for _, n := range []int{p.Max - 1, p.Max, p.Max + 1, 2 * p.Max, 2*p.Max + 1, 4*p.Max - 1} {
		data := pseudoRandomBytes(19, n)
		want := c.Split(data)
		for _, readSize := range []int{1, p.Max, 0} {
			got, _ := collectStream(t, c, data, readSize)
			require.Equal(t, want, got, "n=%d readSize=%d", n, readSize)
		}
	}
}

// TestSplitStream_AllZeros exercises the force-cut path through the streaming loop specifically:
// constant content never produces a content-defined boundary, so every chunk comes out of the
// `len(buf) > Max` branch and the slide-and-refill logic is the only thing making progress.
func TestSplitStream_AllZeros(t *testing.T) {
	t.Parallel()
	c := chunk.New(chunk.DefaultParams())
	data := make([]byte, 1<<20)
	got, _ := collectStream(t, c, data, 4096)
	require.Equal(t, c.Split(data), got)
}

// TestSplitStream_Concurrent asserts SplitStream is re-entrant, which it is only because it
// allocates its refill buffer per call instead of caching one on the chunker.
//
// This is the streaming half of the property TestSplit_Concurrent states for Split, and it is the
// half that actually constrains the implementation: Split reads a caller-owned slice and has
// nothing to share, whereas SplitStream needs a working buffer and the obvious optimization —
// hanging one off the chunker — would corrupt every concurrent call. The daemon splits payloads
// from several sessions at once through one shared Chunker, so that corruption would surface as an
// intermittent bad root hash rather than as a crash.
func TestSplitStream_Concurrent(t *testing.T) {
	t.Parallel()
	c := chunk.New(defaults())

	inputs := make([][]byte, 8)
	want := make([][]chunk.Chunk, len(inputs))
	for i := range inputs {
		inputs[i] = pseudoRandomBytes(byte(40+i), 60000+i*1000)
		want[i] = c.Split(inputs[i])
		require.NotEmpty(t, want[i])
	}

	const goroutines = 16
	errs := make(chan error, goroutines)
	for g := 0; g < goroutines; g++ {
		go func() {
			var out []chunk.Chunk
			err := c.SplitStream(bytes.NewReader(inputs[g%len(inputs)]),
				func(ch chunk.Chunk, _ []byte) error { out = append(out, ch); return nil })
			if err == nil && !equalChunks(out, want[g%len(inputs)]) {
				err = errStreamDiverged
			}
			errs <- err
		}()
	}
	for i := 0; i < goroutines; i++ {
		require.NoError(t, <-errs)
	}
}

// errStreamDiverged reports a concurrent SplitStream that disagreed with Split.
var errStreamDiverged = errors.New("concurrent SplitStream diverged from Split")

// equalChunks reports whether two chunk lists are elementwise identical.
func equalChunks(a, b []chunk.Chunk) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

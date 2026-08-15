package chunk

import (
	"crypto/sha256"
	"errors"
	"io"

	"github.com/qompack/qompack/internal/core"
)

// maxEmptyReads bounds how many consecutive (0, nil) results SplitStream will tolerate from a
// reader before giving up with io.ErrNoProgress. io.Reader discourages that result but does not
// forbid it, and a chunker that spun on it would hang inside a hook — §12.3 is explicit that the
// hot path is budgeted in milliseconds and that a hook which never returns costs the user their
// turn. The limit matches bufio's own maxConsecutiveEmptyReads, for the same reason bufio has one.
const maxEmptyReads = 100

// SplitStream reads r to completion and calls fn once per chunk, in order, with the chunk's
// metadata and its bytes. It produces exactly the chunk sequence Split would produce over the same
// bytes, for every possible pattern of Read sizes — store's ingest path streams and its verifier
// batches, so any divergence would make a stored object fail to reproduce its own root hash.
//
// # The payload aliases an internal buffer
//
// The []byte handed to fn points into SplitStream's own working buffer and is valid ONLY for the
// duration of the call. SplitStream overwrites it as soon as fn returns. A callback that needs the
// bytes afterwards must copy them. This is deliberate: streaming exists so that a large payload
// never has to be resident all at once, and handing out a fresh copy per chunk would reintroduce
// exactly the allocation the streaming path was written to avoid.
//
// # A nil reader is not an error
//
// The first thing SplitStream does is return nil for a nil r. That is a contract with
// test/guards' TestAllStubsReturnNotImplemented, which walks every seam in the tree reflectively
// and calls each error-returning method with ZERO-VALUED arguments — for this method, a nil
// io.Reader and a nil callback. It requires the call not to panic and the error to be either nil
// or a wrapped core.ErrNotImplemented. There is nothing to read from a nil reader and nothing has
// gone wrong, so nil is the honest answer. A nil fn with a REAL reader is a different situation
// entirely — it would consume and silently discard the caller's whole stream — and is reported.
//
// # Errors
//
// An error from fn aborts immediately and is returned unwrapped, so a caller can still match its
// own sentinels with errors.Is. A read error other than io.EOF is likewise returned unwrapped.
// io.ErrUnexpectedEOF is treated as a clean end of stream only when it arrives alongside bytes that
// were actually read; on its own it means the stream was truncated before anything arrived, which
// is a failure rather than an empty input.
func (c *chunker) SplitStream(r io.Reader, fn func(Chunk, []byte) error) error {
	if r == nil {
		return nil
	}
	if fn == nil {
		return errors.New("chunk: SplitStream: fn must not be nil")
	}

	// Capacity 2*Max, and it never grows. The refill loop stops as soon as more than Max bytes are
	// buffered, and a single Read can add at most the remaining capacity, so the buffer holds at
	// most 2*Max bytes at any moment — which is also why Normalized caps Max at 16*Target: without
	// that, a config could ask for an arbitrarily large hot-path allocation here.
	buf := make([]byte, 0, c.p.Max*2)
	digest := sha256.New()

	var (
		offset int64
		hashed core.Hash
		eof    bool
		empty  int
	)
	for {
		// Refill. The condition is `<= Max`, i.e. read until STRICTLY more than Max bytes are
		// buffered, because at exactly Max bytes nextCut cannot yet distinguish a real force-cut at
		// Max from a content boundary one byte later — and an emit test of `>=` here would emit a
		// chunk, slide, and re-enter the refill having consumed input it should have waited on.
		for !eof && len(buf) <= c.p.Max {
			n, err := r.Read(buf[len(buf):cap(buf)])
			buf = buf[:len(buf)+n]
			switch {
			case n > 0:
				empty = 0
			case err == nil:
				empty++
				if empty >= maxEmptyReads {
					return io.ErrNoProgress
				}
			}
			if err != nil {
				switch {
				case errors.Is(err, io.EOF):
					eof = true
				case errors.Is(err, io.ErrUnexpectedEOF) && n > 0:
					// Bytes arrived alongside the error: the stream ended, it was not empty.
					eof = true
				default:
					return err
				}
			}
		}

		// Emit. Every chunk whose boundary is already decidable: either more than Max bytes are
		// buffered (nextCut can see its whole search window, so its answer cannot change) or the
		// stream has ended (what is buffered is all there will ever be).
		for len(buf) > c.p.Max || (eof && len(buf) > 0) {
			n := c.nextCut(buf)
			hashInto(digest, buf[:n], &hashed)
			if err := fn(Chunk{Offset: offset, Len: n, Hash: hashed}, buf[:n]); err != nil {
				return err
			}
			offset += int64(n)
			// Slide: move the unconsumed tail to the front. copy handles the overlap, and reusing
			// the same backing array is what makes the aliasing contract above true.
			buf = buf[:copy(buf, buf[n:])]
		}

		if eof && len(buf) == 0 {
			return nil
		}
	}
}

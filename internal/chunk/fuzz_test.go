package chunk_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/qompack/qompack/internal/chunk"
)

// corpusDir is testdata/corpora/chunk/, the seed corpus this subplan owns. Its six files are the
// content classes a chunker behaves differently on, and every one of them has bitten a real CDC
// implementation at some point:
//
//	zeros-64k.bin                  a constant run — no content boundary exists, so every chunk is
//	                               force-cut at Max and the rolling hash sits on a fixed point
//	ones-64k.bin                   the same, with the complementary byte, so a table indexing bug
//	                               that happened to be benign at 0x00 is not benign here
//	incompressible-seed3-64k.bin   uniform noise — boundaries fire at their natural rate
//	source-200k.go.txt             real UTF-8 Go source: low entropy, long repeated substrings,
//	                               which is what the tool output this store actually holds looks like
//	small-4k.bin                   shorter than Max but longer than Min: exactly one scan window
//	one-byte.bin                   shorter than the priming window itself
const corpusDir = "../../testdata/corpora/chunk"

// FuzzSplit asserts the invariants Split promises for arbitrary input. There is no oracle for
// "were these the right boundaries" — the boundaries are whatever the gear table says — so the
// target checks the four things that must hold regardless: it never panics, the chunks partition
// the input exactly, every chunk respects [Min, Max] except the last, and splitting the same bytes
// twice gives the same answer.
//
// The no-panic half is the one that matters operationally: Split runs inside a PreToolUse hook, and
// §12.3 is explicit that a hook which panics costs the user their turn. Malformed, truncated and
// adversarial content reaches it constantly, because the content is whatever a tool happened to
// print.
func FuzzSplit(f *testing.F) {
	entries, err := os.ReadDir(corpusDir)
	if err == nil {
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			if b, readErr := os.ReadFile(filepath.Join(corpusDir, e.Name())); readErr == nil {
				f.Add(b)
			}
		}
	}
	// Inline seeds so the target still has coverage of the degenerate lengths even if the corpus
	// directory is unavailable (a bare `go test` run from a copied single file).
	f.Add([]byte(nil))
	f.Add([]byte{})
	f.Add([]byte{0})
	f.Add(bytes.Repeat([]byte{'x'}, 1024))
	f.Add(bytes.Repeat([]byte{'x'}, 1025))
	f.Add(bytes.Repeat([]byte("ab"), 8192))

	p := chunk.DefaultParams()
	c := chunk.New(p)

	f.Fuzz(func(t *testing.T, data []byte) {
		chunks := c.Split(data)

		if len(data) == 0 {
			if chunks != nil {
				t.Fatalf("Split of %d bytes returned %d chunks, want nil", len(data), len(chunks))
			}
			return
		}

		var off int64
		for i, ch := range chunks {
			if ch.Offset != off {
				t.Fatalf("chunk %d starts at %d, want %d", i, ch.Offset, off)
			}
			if ch.Len <= 0 {
				t.Fatalf("chunk %d has length %d", i, ch.Len)
			}
			if ch.Len > p.Max {
				t.Fatalf("chunk %d has length %d, above Max %d", i, ch.Len, p.Max)
			}
			if i < len(chunks)-1 && ch.Len < p.Min {
				t.Fatalf("non-final chunk %d has length %d, below Min %d", i, ch.Len, p.Min)
			}
			off += int64(ch.Len)
		}
		if off != int64(len(data)) {
			t.Fatalf("chunks cover %d bytes, want %d", off, len(data))
		}

		// Coverage: the concatenated chunk payloads must be the input.
		var rebuilt []byte
		for _, ch := range chunks {
			rebuilt = append(rebuilt, data[ch.Offset:int(ch.Offset)+ch.Len]...)
		}
		if !bytes.Equal(data, rebuilt) {
			t.Fatal("chunks do not reassemble the input")
		}

		// Determinism, including across an independently constructed Chunker.
		again := chunk.New(p).Split(data)
		if len(again) != len(chunks) {
			t.Fatalf("second split produced %d chunks, want %d", len(again), len(chunks))
		}
		for i := range chunks {
			if again[i] != chunks[i] {
				t.Fatalf("chunk %d differs between splits: %+v vs %+v", i, again[i], chunks[i])
			}
		}
	})
}

// FuzzSplitStream asserts the streaming path agrees with the batch path for arbitrary input at an
// arbitrary reader granularity. The two paths share nextCut but not their buffering, and the
// buffering is where an off-by-one silently changes an object's root hash rather than crashing.
func FuzzSplitStream(f *testing.F) {
	f.Add([]byte(nil), 1)
	f.Add(bytes.Repeat([]byte{0}, 40000), 3)
	f.Add(bytes.Repeat([]byte("qompack"), 9000), 16384)
	if b, err := os.ReadFile(filepath.Join(corpusDir, "source-200k.go.txt")); err == nil {
		f.Add(b, 4096)
	}

	c := chunk.New(chunk.DefaultParams())

	f.Fuzz(func(t *testing.T, data []byte, readSize int) {
		if readSize <= 0 {
			readSize = 1
		}
		if readSize > 1<<20 {
			readSize = 1 << 20
		}
		want := c.Split(data)

		var got []chunk.Chunk
		err := c.SplitStream(&chunkedReader{data: data, size: readSize},
			func(ch chunk.Chunk, payload []byte) error {
				if len(payload) != ch.Len {
					t.Fatalf("payload length %d does not match chunk length %d", len(payload), ch.Len)
				}
				got = append(got, ch)
				return nil
			})
		if err != nil {
			t.Fatalf("SplitStream: %v", err)
		}
		if len(got) != len(want) {
			t.Fatalf("streamed %d chunks, batch produced %d (readSize=%d)", len(got), len(want), readSize)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("chunk %d differs (readSize=%d): %+v vs %+v", i, readSize, got[i], want[i])
			}
		}
	})
}

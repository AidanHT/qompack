package chunk

import (
	"bytes"
	"crypto/sha256"
	"testing"

	"github.com/qompack/qompack/internal/core"
)

// The benchmarks live in package chunk rather than chunk_test because BenchmarkGearScan_1MiB has to
// call nextCut directly: separating "how fast can the rolling hash find boundaries" from "how fast
// can sha256 hash the result" is the whole point of having it, and from outside the package the two
// are inseparable.
//
// Budgets these are measured against (§12.3's hot-path budget is what they roll up into):
//
//	BenchmarkSplit_100KB           < 800 µs/op
//	BenchmarkGearScan_1MiB         >= 400 MB/s
//	BenchmarkSplit_1MiB            >= 120 MB/s and <= 2 allocs/op
//	BenchmarkSplitStream_4MiB      <= 40 ms/op
//	BenchmarkRootHash_1000Chunks   < 40 µs/op

// benchBytes returns n deterministic, incompressible bytes. Incompressible matters: low-entropy
// input produces long force-cut chunks and would flatter the boundary scan by giving it fewer
// decisions to make per byte.
func benchBytes(n int) []byte {
	out := make([]byte, 0, n+sha256.Size)
	var block [8]byte
	var counter uint64
	for len(out) < n {
		for i := 0; i < 8; i++ {
			block[i] = byte(counter >> (8 * i))
		}
		h := sha256.Sum256(block[:])
		out = append(out, h[:]...)
		counter++
	}
	return out[:n]
}

// BenchmarkSplit_100KB is the size a single tool-output payload typically lands at, so this is the
// number the PreToolUse hook budget is actually spent against.
func BenchmarkSplit_100KB(b *testing.B) {
	c := New(DefaultParams())
	data := benchBytes(100 << 10)
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sinkChunks = c.Split(data)
	}
}

// BenchmarkGearScan_1MiB measures boundary detection alone — no hashing, no slice growth, no
// per-chunk bookkeeping. It is the floor on everything else in this package: Split can never be
// faster than this, so a regression here is a regression everywhere, and separating it out means a
// slowdown can be attributed to the scan or to sha256 rather than guessed at.
func BenchmarkGearScan_1MiB(b *testing.B) {
	c, ok := New(DefaultParams()).(*chunker)
	if !ok {
		b.Fatal("New did not return *chunker")
	}
	data := benchBytes(1 << 20)
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var cuts int
		for off := 0; off < len(data); {
			off += c.nextCut(data[off:])
			cuts++
		}
		sinkInt = cuts
	}
}

// BenchmarkSplit_1MiB is the allocation gate. Split is documented as exactly two allocations — the
// returned []Chunk and one sha256 hasher reused across every chunk — and this is where that is
// enforced. A third allocation would mean either the chunk slice grew (the capacity estimate is
// wrong) or the digest is being summed into a fresh buffer per chunk, which at ~230 chunks per MiB
// is a per-object garbage regression the hot path cannot absorb.
func BenchmarkSplit_1MiB(b *testing.B) {
	c := New(DefaultParams())
	data := benchBytes(1 << 20)
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sinkChunks = c.Split(data)
	}
}

// BenchmarkSplitStream_4MiB measures the streaming path over a payload at the large end of what a
// single tool call can produce, read in realistic 64 KiB gulps.
func BenchmarkSplitStream_4MiB(b *testing.B) {
	c := New(DefaultParams())
	data := benchBytes(4 << 20)
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var n int
		if err := c.SplitStream(bytes.NewReader(data), func(ch Chunk, _ []byte) error {
			n += ch.Len
			return nil
		}); err != nil {
			b.Fatal(err)
		}
		sinkInt = n
	}
}

// BenchmarkRootHash_1000Chunks measures the Merkle root over a large object's chunk list. It runs
// once per stored object, after chunking, so it is small next to Split — but it is also called on
// every staleness check in store, where it runs against a list that is already in memory and must
// stay well under the per-call budget.
func BenchmarkRootHash_1000Chunks(b *testing.B) {
	chunks := make([]Chunk, 1000)
	for i := range chunks {
		var payload [8]byte
		for j := 0; j < 8; j++ {
			payload[j] = byte(i >> (8 * j))
		}
		chunks[i] = Chunk{Offset: int64(i) * 4096, Len: 4096, Hash: sha256.Sum256(payload[:])}
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sinkHash = RootHash(chunks)
	}
}

// Package-level sinks keep the compiler from eliminating the work the benchmarks above measure.
var (
	sinkChunks []Chunk
	sinkInt    int
	sinkHash   core.Hash
)

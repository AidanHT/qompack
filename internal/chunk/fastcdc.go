package chunk

import (
	"crypto/sha256"
	"hash"
	"math/bits"

	"github.com/qompack/qompack/internal/core"
)

// ChunkDomain is the domain-separation string for chunk content hashes. It is an alias of
// core.DomainChunk, never a re-spelling of the literal: internal/core owns the domain registry
// (internal/core/hash.go documents every domain and who owns it), and a package that wrote the
// string out itself could drift from that registry during a version bump and silently re-key every
// object already in the CAS.
const ChunkDomain = core.DomainChunk

// RootDomain is the domain-separation string for the Merkle root over a chunk-hash list, aliased
// from core.DomainRoot for the same reason as ChunkDomain. Keeping the two domains distinct is what
// stops a single-chunk object's root from colliding with its only chunk.
const RootDomain = core.DomainRoot

// chunkHashPrefix is the exact byte sequence core.HashBytes writes ahead of a chunk's payload:
// the domain string followed by the 0x00 separator.
//
// It is precomputed once, at package scope, because Split hashes hundreds of chunks per megabyte
// and calling core.HashBytes per chunk would allocate twice per chunk — once for []byte(domain) and
// once for Sum(nil) — which is exactly the per-chunk garbage the two-allocation budget forbids.
// Writing this prefix and then the payload into a reused hasher produces a byte-identical digest,
// which TestChunkHashMatchesCoreHashBytes proves over a thousand random payloads.
var chunkHashPrefix = []byte(ChunkDomain + "\x00")

// maskWidthDelta is FastCDC's normalization level (NC in Xia et al. 2016, "FastCDC: a Fast and
// Efficient Content-Defined Chunking Approach for Data Deduplication"), here NC=1.
//
// Plain content-defined chunking uses one mask everywhere, which makes chunk length geometrically
// distributed: masses of tiny chunks and a long tail of force-cut ones. Normalized chunking uses
// two — a mask NC bits WIDER before Target, so a cut there is 2^NC times less likely, and a mask NC
// bits NARROWER after it, so a cut there is 2^NC times more likely. The result is a distribution
// squeezed toward Target from both sides.
//
// NC=1 here, DELIBERATELY not the NC=2 the paper recommends. The two settings trade the same thing
// against each other, and this codebase's priority is the opposite of the paper's.
//
// What NC costs is boundary stability, which is the property 00-ARCHITECTURE.md §5.5 actually names
// as normative ("inserting bytes at offset k perturbs at most 2 chunks"). An edit shifts the bytes
// after it across the Target line, where the mask width changes; a boundary that fired under the
// lenient post-Target mask can land in the strict pre-Target region and stop firing, which
// desynchronizes the chunk after it. The wider the gap between the two masks, the more often that
// happens. Measured over 512 random 1-500 byte insertions into 256 KiB buffers
// (TestPropBoundaryStability_Insertion), the share of edits perturbing at most 2 chunks is:
//
//	NC=2   90.4%   (94.3% within 3, worst case 17 novel chunks over 20 000 trials)
//	NC=1   97.1%   (99.4% within 3, worst case  7)
//
// What NC=2 is supposed to buy — a tighter size distribution — does not materialize on this
// workload. Measured on testdata/corpora/toolout plus a 200 KB Go source with one line inserted,
// NC=0, 1, 2 and 3 all yield the same 1 novel chunk out of 40 on the insertion case, and
// corpus-wide dedup ratios differ by under 0.3%. The mean chunk size on real text is in fact
// CLOSER to Target at NC=1 (4333 B) than at NC=2 (3980 B); the paper's variance argument is about
// backup workloads, not about tool output. So NC=2 costs stability and buys nothing measurable
// here.
//
// CHANGING THIS VALUE MOVES EVERY BOUNDARY, exactly like changing gearSeed does — every chunk hash
// in the CAS changes and every root in index/roots.jsonl stops resolving, with no migration short
// of re-ingesting every session. It was chosen before SP-06 stored anything, which was the only
// free moment to choose it. TestSplit_GoldenBoundaries is the tripwire.
const maskWidthDelta = 1

// Chunker implementations are returned by New. The concrete type is unexported; ParamsReporter is
// how a caller reads back what New actually configured.
type chunker struct {
	// p is the NORMALIZED Params — the ones the scan actually runs with, not the ones New was
	// handed.
	p Params
	// maskS is the strict mask, used from Min up to Target. Wider than log2(Target), so cuts before
	// Target are rare.
	maskS uint64
	// maskL is the lenient mask, used from Target up to Max. Narrower than log2(Target), so cuts
	// after Target are common. Its set bits are a subset of maskS's, which means a position that
	// would have cut under maskS always cuts under maskL too — crossing the Target line can add
	// boundaries but never remove one.
	maskL uint64
}

// New returns a Chunker configured by p, normalizing p first (see Params.Normalized) so that any
// Params — including the zero value — yields a working chunker.
//
// New has no error return, matching every other computational constructor in this codebase
// (symbols.New, grammar.New, redact.New): building a Chunker performs no I/O, so there is nothing
// to fail at, and a constructor that rejected an out-of-range config would take the daemon down at
// startup rather than degrading. The Chunker it returns also implements ParamsReporter, so a caller
// that needs to know what its numbers were turned into can ask.
//
// The returned Chunker is immutable and therefore safe for concurrent use by any number of
// goroutines; neither Split nor SplitStream writes to it.
func New(p Params) Chunker {
	np := p.Normalized()
	width := bits.TrailingZeros64(uint64(np.Target))
	return &chunker{
		p:     np,
		maskS: topBits(width + maskWidthDelta),
		maskL: topBits(width - maskWidthDelta),
	}
}

// topBits returns a mask with the n most significant bits set.
//
// The HIGH bits, specifically, and that is the load-bearing detail. Under the recurrence
// fp = (fp<<1) ^ gear[b], bit j of the fingerprint is the XOR of one bit from each of the last j+1
// gear entries — so the high bits summarize a long window and the low bits summarize almost
// nothing. A mask built from the low bits would make the boundary decision depend on one or two
// bytes, and the chunker would re-cut at essentially every byte value, destroying both the size
// distribution and boundary stability.
func topBits(n int) uint64 { return ^uint64(0) << (64 - n) }

// Params returns the effective, post-normalization parameters this Chunker is running with,
// implementing ParamsReporter.
func (c *chunker) Params() Params { return c.p }

// nextCut returns the length of the chunk that starts at data[0].
//
// The scan has three phases, and every one of them is a deliberate trade:
//
//   - Below Min nothing is tested at all. Skipping is what puts a floor under the chunk size, and
//     it is free: it also skips the fingerprint updates, so the shortest chunk is also the cheapest
//     to find.
//   - From Min to Target the strict mask applies, making an early cut unlikely.
//   - From Target to Max the lenient mask applies, making a cut likely and pulling the distribution
//     back toward Target.
//   - At Max the chunk is force-cut whether or not the content agreed. This is what bounds the
//     memory any single chunk can require, and it is the only cut that is not content-defined —
//     which is why constant content (where no mask ever fires) produces exactly Max-sized chunks.
//
// The fingerprint is primed over the GearWindow bytes immediately BEFORE Min rather than rolled
// from data[0]. Both give bit-identical results — after GearWindow bytes the fingerprint has
// forgotten everything earlier (see GearWindow) — but priming reads 64 bytes instead of Min of
// them, which is where most of this package's throughput comes from. Normalized's Min >= GearWindow
// guarantee is what makes the priming index non-negative.
func (c *chunker) nextCut(data []byte) int {
	n := len(data)
	if n <= c.p.Min {
		// Nothing to decide: the first testable position is at index Min, which does not exist.
		return n
	}
	if n > c.p.Max {
		n = c.p.Max
	}
	normal := c.p.Target
	if normal > n {
		normal = n
	}

	var fp uint64
	for j := c.p.Min - GearWindow; j < c.p.Min; j++ {
		fp = (fp << 1) ^ gear[data[j]]
	}

	i := c.p.Min
	for ; i < normal; i++ {
		fp = (fp << 1) ^ gear[data[i]]
		if fp&c.maskS == 0 {
			return i + 1
		}
	}
	for ; i < n; i++ {
		fp = (fp << 1) ^ gear[data[i]]
		if fp&c.maskL == 0 {
			return i + 1
		}
	}
	return n
}

// Split returns the content-defined chunks of data, in order, each carrying its offset, length and
// domain-separated content hash. Split(nil) and Split of an empty slice both return nil — there is
// no such thing as a zero-length chunk, and store's root records distinguish "no chunks" from "one
// empty chunk".
//
// Split performs exactly two allocations regardless of input size: the returned slice and one
// sha256 hasher, reused across every chunk. Getting there needs three things, all of which
// BenchmarkSplit_1MiB enforces. The slice is sized to the exact worst case (len/Min + 1 chunks,
// since only the final chunk may be shorter than Min) so append can never grow it. The domain
// prefix is a package-level []byte rather than a per-call []byte(ChunkDomain) conversion. And the
// digest is summed directly into the Chunk's own Hash field, which already lives in the slice's
// backing array, instead of into a fresh buffer.
//
// Split holds no state across calls and is safe for concurrent use.
func (c *chunker) Split(data []byte) []Chunk {
	if len(data) == 0 {
		return nil
	}

	chunks := make([]Chunk, 0, len(data)/c.p.Min+1)
	digest := sha256.New()
	for off := 0; off < len(data); {
		n := c.nextCut(data[off:])
		chunks = append(chunks, Chunk{Offset: int64(off), Len: n})
		hashInto(digest, data[off:off+n], &chunks[len(chunks)-1].Hash)
		off += n
	}
	return chunks
}

// hashInto resets h, writes the chunk domain prefix and payload through it, and sums the digest
// directly into out. Summing into an existing core.Hash rather than taking h.Sum(nil) is what keeps
// the per-chunk allocation count at zero: out already points into memory the caller owns, and
// Sum appends into its 32 bytes of capacity without allocating.
func hashInto(h hash.Hash, payload []byte, out *core.Hash) {
	h.Reset()
	// hash.Hash documents that Write never returns an error, so there is nothing to handle here.
	_, _ = h.Write(chunkHashPrefix)
	_, _ = h.Write(payload)
	_ = h.Sum(out[:0])
}

package chunk

import (
	"crypto/sha256"

	"github.com/qompack/qompack/internal/core"
)

// RootHash is the Merkle root over chunks: the domain-separated digest of every chunk hash
// concatenated in order (00-ARCHITECTURE.md §5.5: HashBytes(core.DomainRoot, concat(chunk
// hashes))). It is fully specified by the architecture, so SP-01 implements it for real rather
// than stubbing it (00-ARCHITECTURE.md §14.1): SP-06's store needs a working root hash to
// populate index/roots.jsonl long before SP-04's chunker is real.
//
// The concatenation is STREAMED into the digest rather than materialized. sha256 is a streaming
// construction, so writing the domain, the separator and then each 32-byte hash in turn produces
// the identical digest to hashing one buffer holding all of them — TestRootHash_Formula pins that
// equality against core.HashBytes directly, so the two spellings cannot drift apart.
//
// What it saves is a 32 KB allocation and a 32 KB copy per call at the thousand-chunk sizes SP-06
// actually stores, which is most of the distance between this and its budget: raw sha256 over
// those 32 KB costs about 21 us on the reference host and the whole call is budgeted at 40 us
// (plans/V2-SP-04, "Performance budget"), so a redundant buffer build is not affordable. SP-06
// calls this once per stored tool result, on the hot path.
//
// The cost of streaming is that core's domain-separation convention — domain || 0x00 || payload —
// is spelled out a second time here, in a package that does not own it, which is exactly how two
// copies of a wire format drift apart. What makes that safe is not care but coverage:
// TestRootHash_Formula and TestRootHash_Empty both assert equality with core.HashBytes for real
// inputs, so a change to core's domain string or separator byte fails them on the next run rather
// than silently producing roots that no longer match the rest of the system.
func RootHash(chunks []Chunk) core.Hash {
	h := sha256.New()
	_, _ = h.Write([]byte(core.DomainRoot))
	_, _ = h.Write([]byte{domainSeparator})
	for i := range chunks {
		_, _ = h.Write(chunks[i].Hash[:])
	}

	var out core.Hash
	copy(out[:], h.Sum(nil))
	return out
}

// domainSeparator is the 0x00 byte core.HashBytes writes between the domain and the payload. It is
// not a tunable: it is the byte that keeps ("a","bc") and ("ab","c") from colliding, and it is
// duplicated here only so RootHash can stream. See the note on RootHash.
const domainSeparator = 0x00

package chunk

import "github.com/qompack/qompack/internal/core"

// hashSize is len(core.Hash{}) — a sha256 digest is 32 bytes — spelled out as a named constant
// purely so RootHash's preallocation reads as "one hash's width" rather than a bare 32.
const hashSize = 32

// RootHash is the Merkle root over chunks: the domain-separated digest of every chunk hash
// concatenated in order (00-ARCHITECTURE.md §5.5: HashBytes(core.DomainRoot, concat(chunk
// hashes))). It is fully specified by the architecture, so SP-01 implements it for real rather
// than stubbing it (00-ARCHITECTURE.md §14.1): SP-06's store needs a working root hash to
// populate index/roots.jsonl long before SP-04's chunker is real.
func RootHash(chunks []Chunk) core.Hash {
	buf := make([]byte, 0, len(chunks)*hashSize)
	for _, c := range chunks {
		buf = append(buf, c.Hash[:]...)
	}
	return core.HashBytes(core.DomainRoot, buf)
}

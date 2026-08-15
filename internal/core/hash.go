package core

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

// Domain-separation registry.
//
// Every hash in Qompack is domain-separated: HashBytes writes domain || 0x00 || payload, so a
// digest minted for one purpose can never collide with one minted for another. The complete set
// of domains in use, and who owns each:
//
//	qompack.chunk.v1          chunk content hashes            (SP-04)
//	qompack.root.v1           Merkle root over chunk hashes   (SP-04/06)
//	qompack.neg.v1            negative-knowledge bloom key    (SP-09)
//	qompack.decision          decision IDs                    (SP-10)
//	qompack.args.v1           tool-arg digests                (SP-06)
//	qompack.sketch.bloom.v1   Bloom bit indices               (SP-03)
//	qompack.sketch.cms.v1     Count-Min cell indices          (SP-03)
//	qompack.sketch.hll.v1     HyperLogLog register selection  (SP-03)
//
// The three qompack.sketch.* domains are declared as unexported constants in
// internal/sketch/hash.go rather than here, because nothing outside that package may mint a sketch
// index; they are listed above so this registry keeps its claim to completeness.
//
// Changing one of these strings re-keys every derived value already on disk. Treat them as a
// wire format, not as identifiers.
//
// The one deliberate non-member. 00-ARCHITECTURE.md §2.4 defines the IPC endpoint name as the
// first 12 hex chars of sha256(normalizedAbsProjectRoot) — an *undomained* digest. HashBytes
// prepends domain || 0x00 and would therefore produce a different pipe name, so ipc.Resolve
// calls sha256.Sum256 directly and this registry has no qompack.project.v1 entry on purpose.
// Do not "fix" that into HashBytes: it would silently move every project's endpoint.
const (
	// DomainChunk is the domain for chunk content hashes.
	DomainChunk = "qompack.chunk.v1"
	// DomainRoot is the domain for the Merkle root over a chunk-hash list.
	DomainRoot = "qompack.root.v1"
	// DomainNegKnow is the domain for negative-knowledge bloom keys.
	DomainNegKnow = "qompack.neg.v1"
	// DomainDecision is the domain for decision IDs.
	DomainDecision = "qompack.decision"
	// DomainArgs is the domain for tool-argument digests.
	DomainArgs = "qompack.args.v1"
)

// hashTextPrefix is the canonical text prefix for a Hash. It is not a config value and not a
// magic number: it is the wire format every on-disk record in §5 embeds.
const hashTextPrefix = "sha256:"

// hexLen is the number of hex characters in a full sha256 digest.
const hexLen = sha256.Size * 2

// Hash is a sha256 digest. Its canonical text form is "sha256:" followed by 64 lowercase hex
// characters, which is how it appears in every JSONL record and every checkpoint.
type Hash [sha256.Size]byte

// HashBytes returns the domain-separated digest sha256(domain || 0x00 || b). The separator is
// what keeps ("a","bc") and ("ab","c") distinct; see the domain registry above.
func HashBytes(domain string, b []byte) Hash {
	h := sha256.New()
	_, _ = h.Write([]byte(domain))
	_, _ = h.Write([]byte{0x00})
	_, _ = h.Write(b)
	var out Hash
	copy(out[:], h.Sum(nil))
	return out
}

// String returns the canonical text form: "sha256:" + 64 lowercase hex characters.
func (h Hash) String() string { return hashTextPrefix + hex.EncodeToString(h[:]) }

// Short returns the first 12 hex characters, without the "sha256:" prefix. It is the form used
// in tombstones, IPC endpoint names and human-facing output.
func (h Hash) Short() string { return hex.EncodeToString(h[:])[:12] }

// IsZero reports whether h is the zero digest, which is the "unset" sentinel for optional hash
// fields such as ToolUseRecord.Root before a root is known.
func (h Hash) IsZero() bool { return h == Hash{} }

// ParseHash accepts either the canonical "sha256:<hex>" form or a bare 64-character hex string.
// Every failure wraps ErrNotFound so callers can treat an unparseable reference and a missing
// object identically.
func ParseHash(s string) (Hash, error) {
	s = strings.TrimPrefix(s, hashTextPrefix)
	if len(s) != hexLen {
		return Hash{}, fmt.Errorf("%w: hash must be %d hex chars, got %d", ErrNotFound, hexLen, len(s))
	}
	b, err := hex.DecodeString(s)
	if err != nil {
		return Hash{}, fmt.Errorf("%w: %v", ErrNotFound, err)
	}
	var out Hash
	copy(out[:], b)
	return out, nil
}

// MarshalJSON emits the canonical text form. Every on-disk record in §5 embeds hashes as
// strings, so the wire representation is text, never a byte array.
func (h Hash) MarshalJSON() ([]byte, error) { return json.Marshal(h.String()) }

// UnmarshalJSON accepts the canonical text form or a bare hex string.
func (h *Hash) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return fmt.Errorf("%w: hash must be a JSON string: %v", ErrNotFound, err)
	}
	parsed, err := ParseHash(s)
	if err != nil {
		return err
	}
	*h = parsed
	return nil
}

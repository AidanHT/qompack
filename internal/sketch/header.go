package sketch

import "github.com/qompack/qompack/internal/core"

// Kind identifies which sketch algorithm a Header describes.
type Kind uint8

// The five sketch kinds. Values start at 1, not 0, so the zero Kind (as seen in a stub's zero
// Header{}) unambiguously means "no kind set" rather than being mistaken for KindBloom.
const (
	// KindBloom identifies a Bloom filter (tried.bloom).
	KindBloom Kind = iota + 1
	// KindCMS identifies a Count-Min sketch (touch.cms).
	KindCMS
	// KindHLL identifies a HyperLogLog (explore.hll).
	KindHLL
	// KindMisraGries identifies a Misra-Gries top-k counter.
	KindMisraGries
	// KindMinHash identifies a MinHash signature set.
	KindMinHash
)

// HeaderMagic is the fixed 4-byte prefix every sketch header begins with on disk: 'Q','P','K','S'
// ("QPKS"). SP-03's real Save/Load must stamp and verify exactly this value before trusting Ver,
// Kind, Params, Count, Created or CRC32C.
var HeaderMagic = [4]byte{'Q', 'P', 'K', 'S'}

// Header is the on-disk metadata block every persisted Sketch carries (00-ARCHITECTURE.md §5.7).
type Header struct {
	// Magic is HeaderMagic on a well-formed file.
	Magic [4]byte
	// Ver is the format version, bumped on any layout change.
	Ver uint16
	// Kind identifies which sketch algorithm this header describes.
	Kind Kind
	// Params carries the sketch's construction parameters (capacity, fpRate, epsilon, delta,
	// registers, k, …) so a file is self-describing without consulting config.
	Params map[string]float64
	// Count is the number of items added since construction.
	Count uint64
	// Created is when this sketch was constructed.
	Created core.UnixMilli
	// CRC32C is the checksum of the encoded body, checked on Load.
	CRC32C uint32
}

// Sketch is implemented by every persistable sketch type in this package (Bloom, CMS, HLL,
// MisraGries): it can describe its own on-disk Header and marshal/unmarshal its body.
type Sketch interface {
	Header() Header
	MarshalBinary() ([]byte, error)
	UnmarshalBinary([]byte) error
}

// Save writes s to p atomically (via paths.WriteAtomic), except sketches/tried.bloom, which goes
// through paths.ReplaceBloom instead (00-ARCHITECTURE.md §3.3). Save always reports
// core.ErrNotImplemented until SP-03 lands the real encode-plus-atomic-write implementation.
func Save(p string, s Sketch) error {
	return core.ErrNotImplemented
}

// Load reads p into s, checking CRC32C and Ver; a corrupt or unreadable file reports
// core.ErrNotFound and a Loud log line in the real implementation. Load always reports
// core.ErrNotImplemented until SP-03 lands the real decode-plus-verification implementation.
func Load(p string, s Sketch) error {
	return core.ErrNotImplemented
}

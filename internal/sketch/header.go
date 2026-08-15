package sketch

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"math"
	"slices"
	"strconv"

	"github.com/qompack/qompack/internal/core"
)

// The QPKS frame, byte for byte. All multi-byte integers are little-endian.
//
//	offset  size          field
//	0       4             Magic          'Q'(0x51) 'P'(0x50) 'K'(0x4B) 'S'(0x53)
//	4       2             Ver            uint16, currently 1 (FormatVersion)
//	6       1             Kind           uint8, 1..5; 0 is invalid
//	7       1             Reserved       uint8, must be 0 on write, ignored on read
//	8       8             Count          uint64
//	16      8             Created        int64 (core.UnixMilli)
//	24      4             ParamCount     uint32, ≤ 64
//	28      4             BodyLen        uint32, ≤ MaxFrameBytes
//	32      variable      Params         ParamCount entries, sorted ascending by name (bytewise):
//	                                       1 byte  NameLen (1..32)
//	                                       N bytes Name    (ASCII, [a-z0-9.] only)
//	                                       8 bytes float64 (math.Float64bits, little-endian)
//	32+P    BodyLen       Body           sketch-specific payload
//	end-4   4             CRC32C         uint32, Castagnoli over bytes [0, len-4)
//
// Why an explicit version and CRC rather than encoding/gob: gob's wire format is tied to Go's type
// definitions, so adding a field silently changes what old files mean and a truncated file decodes
// into a plausible half-sketch. Here the layout is a fixed contract, every length is declared and
// cross-checked, and bit rot is caught rather than acted on.
//
// Params are sorted so that MarshalBinary is byte-stable for a given logical state. That is what
// makes the frozen golden fixtures and the marshal∘unmarshal∘marshal identity test mean anything:
// Go randomizes map iteration order on purpose, so without the sort the same sketch would produce
// different bytes on every run.
const (
	// magicLen is the length of the "QPKS" prefix.
	magicLen = 4
	// headerPrefixLen is the fixed-size part of the frame, before the params block.
	headerPrefixLen = 32
	// crcLen is the length of the trailing CRC32C.
	crcLen = 4
	// minFrameLen is the shortest possible frame: the fixed prefix with no params, no body, and
	// the trailing CRC.
	minFrameLen = headerPrefixLen + crcLen
	// paramValueLen is the encoded width of a param value (a float64, math.Float64bits).
	paramValueLen = 8
	// maxParams bounds the params block so that a forged ParamCount cannot make the decoder walk
	// forever. No sketch in this package declares more than four.
	maxParams = 64
	// maxParamNameLen bounds one param name.
	maxParamNameLen = 32
)

// Field offsets inside the fixed 32-byte prefix. They are named rather than spelled inline so the
// encoder and the decoder provably agree on every one of them.
const (
	offVer        = 4
	offKind       = 6
	offReserved   = 7
	offCount      = 8
	offCreated    = 16
	offParamCount = 24
	offBodyLen    = 28
)

// crcTable is the Castagnoli (CRC-32C) polynomial table. It is built once and never written to
// again, so it is not the package-level mutable state this package otherwise refuses; CRC-32C is
// chosen over IEEE because it has hardware support on every architecture Qompack ships to, which
// keeps the checksum off the critical path of a 32 MiB Bloom save.
var crcTable = crc32.MakeTable(crc32.Castagnoli)

// FormatVersion is the QPKS layout version this build writes. Readers accept Ver ≤ FormatVersion
// and never above: a newer plugin's file must not be silently half-read by an older binary.
//
// Upgrade path. Every UnmarshalBinary switches on h.Ver: `case 1:` decodes the layout documented
// above, `default:` returns ErrUnsupportedVersion. When the layout ever changes, FormatVersion
// becomes 2, a `case 2:` arm is added, and the `case 1:` arm stays forever — sketches are
// permanent memory (§6.2), so the oldest file on disk must still decode.
const FormatVersion uint16 = 1

// TriedBloomBase is the base name of the negative-knowledge Bloom filter under .qompack/sketches/.
// It is named here because it is the one sketch path that is append-only (00-ARCHITECTURE.md
// §3.3): paths.WriteAtomic refuses it outright, and Save routes it through ReplaceGenerational.
const TriedBloomBase = "tried.bloom"

// Kind identifies which sketch algorithm a Header describes.
type Kind uint8

// KindInvalid is the zero Kind. It names the value a zeroed region of disk, or a Header{} that
// nothing has filled in, decodes to — so that "unset" is a case the decoder rejects explicitly
// rather than a value that happens to mean something.
const KindInvalid Kind = 0

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

// String returns the short, stable name of k for log lines and error messages. An unrecognised
// Kind renders as kind(N) rather than as a guess, so a forged byte is legible in a Loud log
// instead of being reported as some other sketch.
func (k Kind) String() string {
	switch k {
	case KindInvalid:
		return "invalid"
	case KindBloom:
		return "bloom"
	case KindCMS:
		return "cms"
	case KindHLL:
		return "hll"
	case KindMisraGries:
		return "misragries"
	case KindMinHash:
		return "minhash"
	default:
		return "kind(" + strconv.Itoa(int(k)) + ")"
	}
}

// Valid reports whether k is one of the five defined kinds. The decoder calls it before it trusts
// any length in the frame, because an unset or forged Kind means the bytes are not a sketch this
// build knows how to read.
func (k Kind) Valid() bool { return k >= KindBloom && k <= KindMinHash }

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

// HeaderMagic is the fixed 4-byte prefix every sketch header begins with on disk: 'Q','P','K','S'
// ("QPKS"). Save/Load stamp and verify exactly this value before trusting Ver, Kind, Params,
// Count, Created or CRC32C.
var HeaderMagic = [4]byte{'Q', 'P', 'K', 'S'}

// Sketch is implemented by every persistable sketch type in this package (Bloom, CMS, HLL,
// MisraGries): it can describe its own on-disk Header and marshal/unmarshal its body.
//
// Header() on a live sketch returns CRC32C == 0. The checksum covers the encoded body and cannot
// be known without marshalling, so only DecodeHeader ever populates it; a caller comparing a live
// sketch's CRC32C against a file's is comparing zero against a real number.
//
// No implementation of this interface is safe for concurrent use. The daemon (SP-05) owns every
// live sketch behind its session-registry mutex.
type Sketch interface {
	Header() Header
	MarshalBinary() ([]byte, error)
	UnmarshalBinary([]byte) error
}

// EncodeHeader writes the fixed 32-byte prefix, the params sorted ascending by name, the body, and
// then the CRC32C over everything before it, returning the complete frame.
//
// It returns ErrTooLarge if the assembled frame would exceed MaxFrameBytes, and ErrMalformed if
// there are more than 64 params or if any param name is empty, longer than 32 bytes, or contains a
// byte outside [a-z0-9.]. It returns an error rather than a bare []byte precisely so that the
// single frame-size guard lives here: all five MarshalBinary implementations inherit it by
// construction instead of each re-implementing it, which is what keeps "every constructible sketch
// is also marshallable" true (see the ceiling rule in errors.go).
//
// Two input fields are not written verbatim. Magic is always HeaderMagic, so no caller can emit a
// frame this package would then refuse to read. CRC32C is ignored, because the computed value is
// the only correct one. Ver and Kind, by contrast, ARE written verbatim rather than forced to
// FormatVersion: this function is the framing layer, the sketch types are what decide they are
// version 1 Blooms, and the version- and kind-rejection tests need a way to build a frame this
// build's decoder will refuse.
//
// EncodeHeader is pure and safe for concurrent use.
func EncodeHeader(h Header, body []byte) ([]byte, error) {
	if len(h.Params) > maxParams {
		return nil, fmt.Errorf("%w: %d params exceeds the limit of %d", ErrMalformed, len(h.Params), maxParams)
	}
	// Checked before the arithmetic below so that total cannot overflow int on a 32-bit build.
	if len(body) > MaxFrameBytes {
		return nil, fmt.Errorf("%w: body of %d bytes exceeds MaxFrameBytes (%d)", ErrTooLarge, len(body), MaxFrameBytes)
	}

	names := make([]string, 0, len(h.Params))
	paramBytes := 0
	for name := range h.Params {
		if !paramNameOK(name) {
			return nil, fmt.Errorf("%w: param name %q is not 1..%d bytes of [a-z0-9.]", ErrMalformed, name, maxParamNameLen)
		}
		names = append(names, name)
		paramBytes += 1 + len(name) + paramValueLen
	}
	slices.Sort(names)

	total := headerPrefixLen + paramBytes + len(body) + crcLen
	if total > MaxFrameBytes {
		return nil, fmt.Errorf("%w: frame of %d bytes exceeds MaxFrameBytes (%d)", ErrTooLarge, total, MaxFrameBytes)
	}

	frame := make([]byte, headerPrefixLen, total)
	copy(frame[0:magicLen], HeaderMagic[:])
	binary.LittleEndian.PutUint16(frame[offVer:offKind], h.Ver)
	frame[offKind] = byte(h.Kind)
	frame[offReserved] = 0
	binary.LittleEndian.PutUint64(frame[offCount:offCreated], h.Count)
	binary.LittleEndian.PutUint64(frame[offCreated:offParamCount], uint64(h.Created))
	binary.LittleEndian.PutUint32(frame[offParamCount:offBodyLen], uint32(len(names)))
	binary.LittleEndian.PutUint32(frame[offBodyLen:headerPrefixLen], uint32(len(body)))

	for _, name := range names {
		frame = append(frame, byte(len(name)))
		frame = append(frame, name...)
		frame = binary.LittleEndian.AppendUint64(frame, math.Float64bits(h.Params[name]))
	}
	frame = append(frame, body...)
	return binary.LittleEndian.AppendUint32(frame, crc32.Checksum(frame, crcTable)), nil
}

// DecodeHeader parses a QPKS frame and returns its header and its body.
//
// It runs two passes, in this exact order, and the order is load-bearing — every rejection test in
// header_test.go pins one step of it, because a check that ran late would let a forged length be
// acted on before the check that would have caught it.
//
// Pass 1 validates and measures WITHOUT ALLOCATING, so that every rejection path — which is the
// whole of the fuzz surface — costs zero allocations and cannot be turned into a memory-pressure
// attack by feeding the daemon garbage:
//
//	len(b) < minFrameLen                           → ErrTruncated
//	len(b) > MaxFrameBytes                         → ErrTooLarge
//	b[0:4] != HeaderMagic                          → ErrBadMagic
//	Ver == 0 || Ver > FormatVersion                → ErrUnsupportedVersion
//	!Kind.Valid()                                  → ErrMalformed
//	ParamCount > maxParams                         → ErrMalformed
//	walking the params block: any NameLen == 0 or > 32, any name byte outside [a-z0-9.],
//	  names not strictly ascending                 → ErrMalformed
//	the params block running past len(b)-4         → ErrTruncated
//	BodyLen > MaxFrameBytes                        → ErrTooLarge
//	32+paramBytes+BodyLen != len(b)-4              → ErrTruncated
//	stored CRC != computed CRC                     → ErrCorrupt
//
// This is why every return below is a BARE sentinel rather than a fmt.Errorf wrap: wrapping
// allocates, and TestHeader_RejectLyingBodyLen asserts testing.AllocsPerRun == 0 on the rejection
// path. Byte 7 is the reserved byte: any value is accepted on read, so that a future flag byte can
// be introduced without a version bump for readers that ignore it.
//
// Pass 2 runs only after every check above has passed. It allocates the Params map with
// make(map[string]float64, ParamCount) and fills it.
//
// # Params normalization on a zero-param frame
//
// A frame with ParamCount == 0 decodes to Params == nil, NOT to an empty non-nil map. This is a
// deliberate, load-bearing choice, because EncodeHeader cannot distinguish the two on the way out
// — len(nil) and len(map[string]float64{}) are both 0, so both encode to ParamCount == 0 — and
// something therefore has to be normalized on the way back in. nil is the right target for three
// reasons:
//
//   - It makes the common literal round-trip EXACTLY. A Header written without a Params field at
//     all, and the zero Header{}, both carry nil; those encode and decode back to nil unchanged.
//     Choosing the empty map instead would break that far more frequent case in order to fix the
//     rarer one, and a round-trip test asserting require.Equal on Params would fail on a
//     difference that is not real.
//   - It is safe to consume. Reading from a nil map is legal in Go, so Param and MustParamInt
//     behave identically on nil and on empty: Param reports (0, false) and MustParamInt reports
//     ErrMalformed, which is what a caller asking for an absent param should get either way.
//   - It costs nothing. The alternative allocates a map on every successful decode that will
//     never hold anything.
//
// The case is in any event unreachable from this package's own writers: every sketch defined here
// declares at least one param (Bloom writes m/k/capacity/fprate, CMS width/depth, HLL registers,
// Misra-Gries k, MinHash perms), so only a hand-built frame can produce it. TestHeader_ZeroParams
// pins both halves — the nil round-trip and the empty-map normalization.
//
// The returned Header has CRC32C set to the stored value. The returned body is a SUBSLICE of b:
// callers that retain it must copy, and every UnmarshalBinary in this package does.
//
// DecodeHeader is pure and safe for concurrent use.
func DecodeHeader(b []byte) (Header, []byte, error) {
	if len(b) < minFrameLen {
		return Header{}, nil, ErrTruncated
	}
	if len(b) > MaxFrameBytes {
		return Header{}, nil, ErrTooLarge
	}
	if !bytes.Equal(b[0:magicLen], HeaderMagic[:]) {
		return Header{}, nil, ErrBadMagic
	}
	ver := binary.LittleEndian.Uint16(b[offVer:offKind])
	if ver == 0 || ver > FormatVersion {
		return Header{}, nil, ErrUnsupportedVersion
	}
	kind := Kind(b[offKind])
	if !kind.Valid() {
		return Header{}, nil, ErrMalformed
	}
	paramCount := binary.LittleEndian.Uint32(b[offParamCount:offBodyLen])
	if paramCount > maxParams {
		return Header{}, nil, ErrMalformed
	}

	// Walk the params block by index. Nothing here converts a name to a string or appends to a
	// slice, because either would allocate on a path that must not.
	limit := len(b) - crcLen
	off := headerPrefixLen
	prevStart, prevEnd := 0, 0
	for i := uint32(0); i < paramCount; i++ {
		if off >= limit {
			return Header{}, nil, ErrTruncated
		}
		nameLen := int(b[off])
		if nameLen == 0 || nameLen > maxParamNameLen {
			return Header{}, nil, ErrMalformed
		}
		if off+1+nameLen+paramValueLen > limit {
			return Header{}, nil, ErrTruncated
		}
		nameStart := off + 1
		nameEnd := nameStart + nameLen
		for j := nameStart; j < nameEnd; j++ {
			if !paramByteOK(b[j]) {
				return Header{}, nil, ErrMalformed
			}
		}
		// Strictly ascending, not merely non-descending: a duplicate name would silently lose one
		// of the two values when pass 2 fills the map.
		if i > 0 && bytes.Compare(b[prevStart:prevEnd], b[nameStart:nameEnd]) >= 0 {
			return Header{}, nil, ErrMalformed
		}
		prevStart, prevEnd = nameStart, nameEnd
		off = nameEnd + paramValueLen
	}
	paramBytes := off - headerPrefixLen

	bodyLen := binary.LittleEndian.Uint32(b[offBodyLen:headerPrefixLen])
	if bodyLen > MaxFrameBytes {
		return Header{}, nil, ErrTooLarge
	}
	if headerPrefixLen+paramBytes+int(bodyLen) != limit {
		return Header{}, nil, ErrTruncated
	}
	stored := binary.LittleEndian.Uint32(b[limit:])
	if crc32.Checksum(b[:limit], crcTable) != stored {
		return Header{}, nil, ErrCorrupt
	}

	// Pass 2: every declared size is now known to agree with the buffer, so allocating on them is
	// safe.
	h := Header{
		Magic:   HeaderMagic,
		Ver:     ver,
		Kind:    kind,
		Count:   binary.LittleEndian.Uint64(b[offCount:offCreated]),
		Created: core.UnixMilli(binary.LittleEndian.Uint64(b[offCreated:offParamCount])),
		CRC32C:  stored,
	}
	// Left nil when paramCount == 0 — see "Params normalization on a zero-param frame" above; this
	// is the documented normalization, not an oversight.
	if paramCount > 0 {
		h.Params = make(map[string]float64, paramCount)
		p := headerPrefixLen
		for i := uint32(0); i < paramCount; i++ {
			nameLen := int(b[p])
			nameStart := p + 1
			nameEnd := nameStart + nameLen
			h.Params[string(b[nameStart:nameEnd])] = math.Float64frombits(binary.LittleEndian.Uint64(b[nameEnd : nameEnd+paramValueLen]))
			p = nameEnd + paramValueLen
		}
	}
	return h, b[off : off+int(bodyLen)], nil
}

// Param returns the value recorded under name, and whether it was present at all. The two are
// distinguishable because a param legitimately set to 0 (a Count-Min built with epsilon 0, say)
// must not read the same as a param the writer never wrote.
func (h Header) Param(name string) (float64, bool) {
	v, ok := h.Params[name]
	return v, ok
}

// MustParamInt reads name as an integer confined to [min, max], returning ErrMalformed if it is
// absent, non-integral, NaN, infinite, or outside the range.
//
// Every UnmarshalBinary sizes its allocation from a param, so this is the guard that stands
// between a forged header and a make() call: a frame claiming registers = 1e18 is rejected here
// rather than turning into an out-of-memory kill of the daemon.
func (h Header) MustParamInt(name string, min, max int) (int, error) {
	v, ok := h.Params[name]
	if !ok {
		return 0, fmt.Errorf("%w: header has no param %q", ErrMalformed, name)
	}
	if math.IsNaN(v) || v != math.Trunc(v) {
		return 0, fmt.Errorf("%w: param %q = %v is not an integer", ErrMalformed, name, v)
	}
	if v < float64(min) || v > float64(max) {
		return 0, fmt.Errorf("%w: param %q = %v is outside [%d, %d]", ErrMalformed, name, v, min, max)
	}
	return int(v), nil
}

// paramNameOK reports whether name is a legal param name: 1..maxParamNameLen bytes drawn from
// [a-z0-9.]. The alphabet is deliberately narrower than the config keys these params mirror, so
// that a name is byte-comparable without a collation question and sorts identically everywhere —
// which is what makes "sorted ascending by name" a portable statement. It is also why bloom.go
// writes "fprate" rather than the camel-cased config key "fpRate".
func paramNameOK(name string) bool {
	if name == "" || len(name) > maxParamNameLen {
		return false
	}
	for i := 0; i < len(name); i++ {
		if !paramByteOK(name[i]) {
			return false
		}
	}
	return true
}

// paramByteOK reports whether c is in the [a-z0-9.] param-name alphabet.
func paramByteOK(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '.'
}

// Save writes s to p atomically (via paths.WriteAtomic), except sketches/tried.bloom, which goes
// through paths.ReplaceBloom instead (00-ARCHITECTURE.md §3.3). It reports core.ErrNotImplemented
// until this subplan's io.go lands the encode-plus-atomic-write half.
func Save(p string, s Sketch) error {
	return core.ErrNotImplemented
}

// Load reads p into s, checking CRC32C and Ver, and maps every corruption sentinel in errors.go to
// core.ErrNotFound — a sketch is a cache (§13 invariant 3), so an unreadable one and a missing one
// are the same event upstream. Load logs nothing, because it has no logger; prefer LoadWithLog,
// which delivers the Loud line §13 invariant 10 requires and is the form every composition root
// must call. It reports core.ErrNotImplemented until this subplan's io.go lands the
// decode-plus-verification half.
func Load(p string, s Sketch) error {
	return core.ErrNotImplemented
}

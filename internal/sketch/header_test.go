package sketch

import (
	"encoding/binary"
	"hash/crc32"
	"math"
	"testing"

	"github.com/qompack/qompack/internal/core"
	"github.com/stretchr/testify/require"
)

// These tests are in package sketch, not package sketch_test, because TestHash128_Stable and
// TestHash128_DomainSeparated pin the unexported hash128 — the function every Bloom bit index and
// every CMS cell index is derived from. Freezing it here, rather than only observing it through a
// Bloom, is what makes a future "harmless" refactor of the mixer fail loudly instead of silently
// re-keying every sketch already on disk.

// TestHeaderMagic pins sketch.HeaderMagic to the exact 4-byte "QPKS" wire-format prefix
// 00-ARCHITECTURE.md §5.7 specifies. Save/Load must stamp and verify exactly this value.
func TestHeaderMagic(t *testing.T) {
	require.Equal(t, [4]byte{'Q', 'P', 'K', 'S'}, HeaderMagic)
	require.Equal(t, "QPKS", string(HeaderMagic[:]))
}

// TestKind_ZeroValueIsUnset asserts the Kind enum starts at 1, so a zero-value Header{} (as
// returned by a live sketch that has not been decoded) never accidentally looks like a real
// KindBloom identification. KindInvalid names that zero explicitly.
func TestKind_ZeroValueIsUnset(t *testing.T) {
	require.NotEqual(t, Kind(0), KindBloom)
	require.NotEqual(t, Kind(0), KindCMS)
	require.NotEqual(t, Kind(0), KindHLL)
	require.NotEqual(t, Kind(0), KindMisraGries)
	require.NotEqual(t, Kind(0), KindMinHash)
	require.Equal(t, Kind(0), KindInvalid)
	require.False(t, KindInvalid.Valid())
	require.True(t, KindBloom.Valid())
	require.True(t, KindMinHash.Valid())
	require.False(t, Kind(6).Valid())
}

// rawParam is one param exactly as it appears on the wire, in the order the caller lists it.
// EncodeHeader sorts and validates, and so can never emit the malformed frames the rejection
// tests need; buildRawFrame writes whatever it is handed.
type rawParam struct {
	name  string
	value float64
}

// buildRawFrame assembles a QPKS frame byte for byte in the caller's param order and appends a
// CORRECT CRC32C. Every structural rejection test below therefore isolates the single check it
// names: the CRC is never the reason a hand-built frame is refused.
func buildRawFrame(ver uint16, kind Kind, count uint64, created core.UnixMilli, params []rawParam, body []byte) []byte {
	frame := make([]byte, 32)
	copy(frame[0:4], HeaderMagic[:])
	binary.LittleEndian.PutUint16(frame[4:6], ver)
	frame[6] = byte(kind)
	frame[7] = 0
	binary.LittleEndian.PutUint64(frame[8:16], count)
	binary.LittleEndian.PutUint64(frame[16:24], uint64(created))
	binary.LittleEndian.PutUint32(frame[24:28], uint32(len(params)))
	binary.LittleEndian.PutUint32(frame[28:32], uint32(len(body)))
	for _, p := range params {
		frame = append(frame, byte(len(p.name)))
		frame = append(frame, p.name...)
		frame = binary.LittleEndian.AppendUint64(frame, math.Float64bits(p.value))
	}
	frame = append(frame, body...)
	crc := crc32.Checksum(frame, crc32.MakeTable(crc32.Castagnoli))
	return binary.LittleEndian.AppendUint32(frame, crc)
}

// validFrame is the reference frame the byte-patching rejection tests start from: two sorted
// params and a two-byte body, i.e. exactly the frame TestHeader_FrameLayout measures at 58 bytes.
func validFrame(t *testing.T) []byte {
	t.Helper()
	b, err := EncodeHeader(Header{
		Ver:     FormatVersion,
		Kind:    KindBloom,
		Count:   7,
		Created: core.UnixMilli(1_700_000_000_000),
		Params:  map[string]float64{"k": 7, "m": 95872},
	}, []byte{0xAA, 0xBB})
	require.NoError(t, err)
	return b
}

// TestHeader_FrameLayout pins the QPKS prefix field by field and offset by offset. It is the
// wire-format contract every other sketch file in this package inherits: a change here silently
// re-interprets every sketch already on disk, which is precisely what FormatVersion exists to
// make impossible.
func TestHeader_FrameLayout(t *testing.T) {
	b, err := EncodeHeader(Header{
		Ver:     1,
		Kind:    KindBloom,
		Count:   7,
		Created: core.UnixMilli(1_700_000_000_000),
		Params:  map[string]float64{"k": 7, "m": 95872},
	}, []byte{0xAA, 0xBB})
	require.NoError(t, err)

	require.Equal(t, "QPKS", string(b[0:4]))
	require.Equal(t, uint16(1), binary.LittleEndian.Uint16(b[4:6]))
	require.Equal(t, byte(1), b[6])
	require.Equal(t, byte(0), b[7], "byte 7 is the reserved zero")
	require.Equal(t, uint64(7), binary.LittleEndian.Uint64(b[8:16]))
	require.Equal(t, uint64(1_700_000_000_000), binary.LittleEndian.Uint64(b[16:24]))
	require.Equal(t, uint32(2), binary.LittleEndian.Uint32(b[24:28]), "ParamCount")
	require.Equal(t, uint32(2), binary.LittleEndian.Uint32(b[28:32]), "BodyLen")

	// Params, sorted ascending by name: "k" at 32, then "m" at 42.
	require.Equal(t, byte(1), b[32], "NameLen of the first param")
	require.Equal(t, "k", string(b[33:34]))
	require.InDelta(t, 7.0, math.Float64frombits(binary.LittleEndian.Uint64(b[34:42])), 0)
	require.Equal(t, byte(1), b[42], "NameLen of the second param")
	require.Equal(t, "m", string(b[43:44]))
	require.InDelta(t, 95872.0, math.Float64frombits(binary.LittleEndian.Uint64(b[44:52])), 0)

	// Body, then the trailing CRC32C over everything before it.
	require.Equal(t, []byte{0xAA, 0xBB}, b[52:54])
	require.Equal(t,
		crc32.Checksum(b[:54], crc32.MakeTable(crc32.Castagnoli)),
		binary.LittleEndian.Uint32(b[54:58]))

	// 32 + (1+1+8) + (1+1+8) + 2 + 4 = 58.
	require.Len(t, b, 58)
}

// TestHeader_ParamsSortedDeterministically is what makes the golden fixtures meaningful: Go's map
// iteration order is deliberately randomized, so without the sort a sketch would marshal to
// different bytes on every run and no byte-for-byte contract fixture could exist at all.
func TestHeader_ParamsSortedDeterministically(t *testing.T) {
	h1 := Header{Ver: FormatVersion, Kind: KindCMS, Params: map[string]float64{}}
	h2 := Header{Ver: FormatVersion, Kind: KindCMS, Params: map[string]float64{}}
	names := []string{"a", "depth", "epsilon", "width", "z9", "n.max"}
	for i, n := range names {
		h1.Params[n] = float64(i)
	}
	for i := len(names) - 1; i >= 0; i-- {
		h2.Params[names[i]] = float64(i)
	}

	first, err := EncodeHeader(h1, []byte("body"))
	require.NoError(t, err)
	for range 32 {
		again, err := EncodeHeader(h2, []byte("body"))
		require.NoError(t, err)
		require.Equal(t, first, again, "insertion order must not reach the wire")
	}
}

// TestHeader_RoundTrip checks the decoder reconstructs every field the encoder wrote, and that
// CRC32C is populated only by the decoder — a live sketch's Header() cannot know it.
func TestHeader_RoundTrip(t *testing.T) {
	in := Header{
		Ver:     FormatVersion,
		Kind:    KindMisraGries,
		Count:   42,
		Created: core.UnixMilli(1_700_000_000_123),
		Params:  map[string]float64{"k": 128, "maxerror": 3.5},
	}
	body := []byte("misra-gries body bytes")

	frame, err := EncodeHeader(in, body)
	require.NoError(t, err)

	got, gotBody, err := DecodeHeader(frame)
	require.NoError(t, err)
	require.Equal(t, HeaderMagic, got.Magic)
	require.Equal(t, in.Ver, got.Ver)
	require.Equal(t, in.Kind, got.Kind)
	require.Equal(t, in.Count, got.Count)
	require.Equal(t, in.Created, got.Created)
	require.Equal(t, in.Params, got.Params)
	require.Equal(t, body, gotBody)

	require.NotZero(t, got.CRC32C)
	require.Equal(t, binary.LittleEndian.Uint32(frame[len(frame)-4:]), got.CRC32C)

	// Param and MustParamInt read what DecodeHeader wrote.
	v, ok := got.Param("k")
	require.True(t, ok)
	require.InDelta(t, 128.0, v, 0)
	_, ok = got.Param("absent")
	require.False(t, ok)

	k, err := got.MustParamInt("k", 1, 1024)
	require.NoError(t, err)
	require.Equal(t, 128, k)
	_, err = got.MustParamInt("k", 1, 127)
	require.ErrorIs(t, err, ErrMalformed)
	_, err = got.MustParamInt("maxerror", 0, 1024)
	require.ErrorIs(t, err, ErrMalformed, "a non-integral param is not an int")
	_, err = got.MustParamInt("absent", 0, 1024)
	require.ErrorIs(t, err, ErrMalformed)
}

// TestHeader_ZeroParams pins the documented normalization for a frame with no params, in both
// directions. EncodeHeader cannot tell a nil map from an empty one — len is 0 either way — so
// something has to be normalized on the way back, and DecodeHeader normalizes to NIL.
//
// The point of pinning it is that commit 6's round-trip tests compare Params with require.Equal,
// which distinguishes nil from empty. Without this test the choice would be an accident of how
// pass 2 happens to be written, and either a later refactor or a later test would "fix" it in the
// wrong direction.
func TestHeader_ZeroParams(t *testing.T) {
	// A Header written without a Params field at all — by far the common literal, and the zero
	// Header{} — round-trips EXACTLY.
	nilFrame, err := EncodeHeader(Header{Ver: FormatVersion, Kind: KindHLL}, []byte("body"))
	require.NoError(t, err)
	require.Equal(t, uint32(0), binary.LittleEndian.Uint32(nilFrame[24:28]), "ParamCount")

	h, body, err := DecodeHeader(nilFrame)
	require.NoError(t, err)
	require.Nil(t, h.Params, "a zero-param frame decodes to nil, not to an empty map")
	require.Equal(t, []byte("body"), body)

	// An explicitly empty map encodes to the identical bytes and normalizes to nil on the way back.
	emptyFrame, err := EncodeHeader(Header{
		Ver:    FormatVersion,
		Kind:   KindHLL,
		Params: map[string]float64{},
	}, []byte("body"))
	require.NoError(t, err)
	require.Equal(t, nilFrame, emptyFrame, "nil and empty must be indistinguishable on the wire")

	h2, _, err := DecodeHeader(emptyFrame)
	require.NoError(t, err)
	require.Nil(t, h2.Params, "an empty map normalizes to nil")

	// nil Params is safe to consume: reading a nil map is legal, so both accessors behave exactly
	// as they would on an empty one. This is what makes nil a sound normalization target.
	v, ok := h.Param("registers")
	require.False(t, ok)
	require.InDelta(t, 0.0, v, 0)
	_, err = h.MustParamInt("registers", MinHLLRegisters, MaxHLLRegisters)
	require.ErrorIs(t, err, ErrMalformed)
}

// TestHeader_RejectShort rejects anything shorter than the 32-byte prefix plus the 4-byte CRC.
func TestHeader_RejectShort(t *testing.T) {
	_, _, err := DecodeHeader(make([]byte, 35))
	require.ErrorIs(t, err, ErrTruncated)
}

// TestHeader_RejectMagic refuses a file that is not a QPKS frame at all, before it reads a single
// length out of it.
func TestHeader_RejectMagic(t *testing.T) {
	b := validFrame(t)
	b[0] = 'X'
	_, _, err := DecodeHeader(b)
	require.ErrorIs(t, err, ErrBadMagic)
}

// TestHeader_RejectVersionZero refuses Ver 0: no writer ever emits it, so it means a corrupt or
// hand-forged frame.
func TestHeader_RejectVersionZero(t *testing.T) {
	b, err := EncodeHeader(Header{Ver: 0, Kind: KindBloom}, nil)
	require.NoError(t, err)
	_, _, err = DecodeHeader(b)
	require.ErrorIs(t, err, ErrUnsupportedVersion)
}

// TestHeader_RejectVersionFuture refuses Ver above FormatVersion: a newer plugin's file must not
// be silently half-read by an older binary.
func TestHeader_RejectVersionFuture(t *testing.T) {
	b, err := EncodeHeader(Header{Ver: FormatVersion + 1, Kind: KindBloom}, nil)
	require.NoError(t, err)
	_, _, err = DecodeHeader(b)
	require.ErrorIs(t, err, ErrUnsupportedVersion)
}

// TestHeader_RejectKindZero refuses the unset Kind, so a zeroed region of disk can never decode
// into a plausible-looking sketch.
func TestHeader_RejectKindZero(t *testing.T) {
	b, err := EncodeHeader(Header{Ver: FormatVersion, Kind: KindInvalid}, nil)
	require.NoError(t, err)
	_, _, err = DecodeHeader(b)
	require.ErrorIs(t, err, ErrMalformed)
}

// TestHeader_RejectUnsortedParams refuses "m" before "k". Sorted order is the byte-stability
// guarantee, so a frame that violates it was not produced by this encoder and is not trusted.
func TestHeader_RejectUnsortedParams(t *testing.T) {
	b := buildRawFrame(FormatVersion, KindBloom, 0, 0,
		[]rawParam{{"m", 95872}, {"k", 7}}, nil)
	_, _, err := DecodeHeader(b)
	require.ErrorIs(t, err, ErrMalformed)

	// Duplicates are not "ascending" either: strictly ascending, not merely non-descending.
	dup := buildRawFrame(FormatVersion, KindBloom, 0, 0,
		[]rawParam{{"k", 7}, {"k", 8}}, nil)
	_, _, err = DecodeHeader(dup)
	require.ErrorIs(t, err, ErrMalformed)
}

// TestHeader_RejectBadParamName refuses an uppercase name on read. The alphabet is [a-z0-9.] on
// both sides of the wire, so decode cannot admit a name encode would have refused.
func TestHeader_RejectBadParamName(t *testing.T) {
	b := buildRawFrame(FormatVersion, KindBloom, 0, 0, []rawParam{{"K", 7}}, nil)
	_, _, err := DecodeHeader(b)
	require.ErrorIs(t, err, ErrMalformed)

	empty := buildRawFrame(FormatVersion, KindBloom, 0, 0, []rawParam{{"", 7}}, nil)
	_, _, err = DecodeHeader(empty)
	require.ErrorIs(t, err, ErrMalformed)
}

// TestHeader_EncodeRejectsBadParamName is the encoder half of the same alphabet rule, and is why
// bloom.go must write "fprate" rather than the camel-cased config key "fpRate".
func TestHeader_EncodeRejectsBadParamName(t *testing.T) {
	b, err := EncodeHeader(Header{
		Ver:    FormatVersion,
		Kind:   KindBloom,
		Params: map[string]float64{"fpRate": 0.01},
	}, nil)
	require.ErrorIs(t, err, ErrMalformed)
	require.Nil(t, b)

	tooLong, err := EncodeHeader(Header{
		Ver:    FormatVersion,
		Kind:   KindBloom,
		Params: map[string]float64{"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa": 1},
	}, nil)
	require.ErrorIs(t, err, ErrMalformed)
	require.Nil(t, tooLong)
}

// TestHeader_EncodeRejectsOversizeFrame is the single frame-size guard all five MarshalBinary
// implementations inherit: a sketch that cannot be re-read must never be written.
func TestHeader_EncodeRejectsOversizeFrame(t *testing.T) {
	b, err := EncodeHeader(Header{Ver: FormatVersion, Kind: KindBloom}, make([]byte, MaxFrameBytes))
	require.ErrorIs(t, err, ErrTooLarge)
	require.Nil(t, b)
}

// TestHeader_RejectLyingBodyLen is the fuzz-surface test. BodyLen is set BELOW MaxFrameBytes on
// purpose, so the failure is the length-consistency check and not the ErrTooLarge check that
// precedes it — and the whole rejection path must cost zero allocations, because pass 1 validates
// and measures before the Params map is ever made.
func TestHeader_RejectLyingBodyLen(t *testing.T) {
	b := validFrame(t)
	binary.LittleEndian.PutUint32(b[28:32], 1<<20)
	_, _, err := DecodeHeader(b)
	require.ErrorIs(t, err, ErrTruncated)

	require.Zero(t, testing.AllocsPerRun(100, func() {
		_, _, _ = DecodeHeader(b)
	}), "pass 1 must reject without allocating")
}

// TestHeader_RejectOversizeBodyLen is the ordering companion to the row above: at
// MaxFrameBytes+1 the ceiling check fires first and names the real problem.
func TestHeader_RejectOversizeBodyLen(t *testing.T) {
	b := validFrame(t)
	binary.LittleEndian.PutUint32(b[28:32], MaxFrameBytes+1)
	_, _, err := DecodeHeader(b)
	require.ErrorIs(t, err, ErrTooLarge)
}

// TestHeader_RejectOversizeFrame bounds the decoder's input before it reads any length out of it.
func TestHeader_RejectOversizeFrame(t *testing.T) {
	_, _, err := DecodeHeader(make([]byte, MaxFrameBytes+1))
	require.ErrorIs(t, err, ErrTooLarge)
}

// TestHeader_DetectsSingleBitFlip is why the format carries a CRC32C at all: silent bit rot in a
// sketch would degrade retrieval quality with no error anywhere, which §13 invariant 10 forbids.
func TestHeader_DetectsSingleBitFlip(t *testing.T) {
	b := validFrame(t)
	b[40] ^= 1 << 3
	_, _, err := DecodeHeader(b)
	require.ErrorIs(t, err, ErrCorrupt)
}

// TestHash128_Stable freezes hash128 as a golden-in-source constant. Every Bloom bit index and
// every CMS cell index in every sketch already on disk is derived from these two words: if this
// test ever fails, the change that caused it invalidated every persisted sketch, and the fix is to
// revert the change, not to update the constants.
func TestHash128_Stable(t *testing.T) {
	// sha256("qompack.sketch.bloom.v1" || 0x00 || "src/auth.ts"), first two little-endian words,
	// with bit 0 of the second forced to 1.
	const (
		wantH1 = 0xdabbc347540beacf
		wantH2 = 0x201091f037607f53
	)
	h1, h2 := hash128(domainBloom, []byte("src/auth.ts"))
	require.Equal(t, uint64(wantH1), h1)
	require.Equal(t, uint64(wantH2), h2)
	require.Equal(t, uint64(1), h2&1, "h2 is forced odd so the double-hashing stride is never 0")
}

// TestHash128_DomainSeparated proves the bloom and CMS domains cannot collide: the same key must
// land in unrelated places in the two sketches, or their errors would correlate.
func TestHash128_DomainSeparated(t *testing.T) {
	key := []byte("src/auth.ts")
	b1, b2 := hash128(domainBloom, key)
	c1, c2 := hash128(domainCMS, key)
	l1, _ := hash128(domainHLL, key)

	require.NotEqual(t, b1, c1)
	require.NotEqual(t, b1, l1)
	require.NotEqual(t, c1, l1)
	require.Equal(t, uint64(1), b2&1)
	require.Equal(t, uint64(1), c2&1)
}

// TestSplitMix64_Frozen freezes splitmix64. It derives every MinHash permutation coefficient, so a
// change to it changes every Signature ever stored and every near-duplicate decision the store has
// already recorded. Like TestHash128_Stable, this is a revert-the-change test, not an
// update-the-constants test.
//
// splitmix64(0) is cross-checked against the published first output of SplitMix64 seeded with 0,
// so this table is anchored to the reference algorithm and not merely to itself.
func TestSplitMix64_Frozen(t *testing.T) {
	require.Equal(t, uint64(0xe220a8397b1dcdaf), splitmix64(0),
		"published SplitMix64 seed-0 first output; a mismatch means this is no longer SplitMix64")

	for _, tc := range []struct {
		in   uint64
		want uint64
	}{
		{0, 0xe220a8397b1dcdaf},
		{1, 0x910a2dec89025cc1},
		{2, 0x975835de1c9756ce},
		{0xdeadbeef, 0x4adfb90f68c9eb9b},
		{0x9e3779b97f4a7c15, 0x6e789e6aa1b965f4}, // the increment itself, i.e. a doubled add
		{1 << 63, 0x481ec0a212a9f3db},            // high bit only
		{math.MaxUint64, 0xe4d971771b652c20},     // wraps the += on the way in
	} {
		require.Equalf(t, tc.want, splitmix64(tc.in), "splitmix64(0x%016x)", tc.in)
	}

	// Pinned as a generator too, which is how permutation coefficients are derived: feeding the
	// output back in must walk this exact sequence.
	x := uint64(0)
	for _, want := range []uint64{0xe220a8397b1dcdaf, 0xa706dd2f4d197e6f, 0x238275bc38fcbe91} {
		x = splitmix64(x)
		require.Equal(t, want, x)
	}
}

// TestFNV1a64_Frozen freezes fnv1a64, the MinHash shingle hash. Signatures are persisted in an
// append-only index, so a changed shingle hash makes every stored signature incomparable with
// every new one — silently, since both sides still produce plausible numbers.
//
// The empty-input and "a" values are the published FNV-1a 64 test vectors, which anchors the table
// to the reference algorithm rather than to this implementation's behaviour.
func TestFNV1a64_Frozen(t *testing.T) {
	require.Equal(t, uint64(0xcbf29ce484222325), fnv1a64(nil),
		"the FNV-1a 64 offset basis; empty input must return it unchanged")
	require.Equal(t, uint64(0xaf63dc4c8601ec8c), fnv1a64([]byte("a")),
		"the published FNV-1a 64 vector for \"a\"")

	for _, tc := range []struct {
		in   string
		want uint64
	}{
		{"", 0xcbf29ce484222325},
		{"a", 0xaf63dc4c8601ec8c},
		{"b", 0xaf63df4c8601f1a5},
		{"ab", 0x089c4407b545986a},
		{"hello", 0xa430d84680aabd0b},
		{"src/auth.ts", 0xfd704eea30dc76c3},
		{"abcdefgh", 0x25da8c1836a8d66d}, // an 8-byte shingle, the DefaultShingleSize case
	} {
		require.Equalf(t, tc.want, fnv1a64([]byte(tc.in)), "fnv1a64(%q)", tc.in)
	}

	// A nil slice and an empty slice are the same input; a zero BYTE is not. Were fnv1a64 ever
	// rewritten to treat a []byte as a C string, these two would collapse together and every
	// shingle containing a NUL would start colliding.
	require.Equal(t, fnv1a64(nil), fnv1a64([]byte{}))
	require.NotEqual(t, fnv1a64([]byte{}), fnv1a64([]byte{0x00}))
	require.Equal(t, uint64(0xaf63bd4c8601b7df), fnv1a64([]byte{0x00}))

	// Order-sensitive: shingles are sequences, so "ab" and "ba" must not share a hash.
	require.NotEqual(t, fnv1a64([]byte("ab")), fnv1a64([]byte("ba")))
}

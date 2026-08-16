// Package sketchtest is the conformance suite for the sketch package (00-ARCHITECTURE.md §5.22):
// every implementation SP-03 ships must pass RunSketchSuite, plus the per-sketch-type suites below
// (RunBloomSuite, RunCMSSuite, RunHLLSuite, RunMisraGriesSuite, RunMinHashSuite) that exercise the
// accuracy and algebra guarantees specific to each algorithm.
//
// SP-01 shipped every suite with its /behaviour block gated behind a Rule W-1 skip, because no
// sketch had an implementation yet. SP-03 owns the package, so the gate is gone: every block below
// runs unconditionally, no skip of any kind remains anywhere under internal/sketch, and a sketch
// that cannot satisfy the contract now fails rather than being excused.
//
// Each suite is factory-driven so that SP-16's future per-segment bloom, and any warm-started
// Count-Min, can be held to the same contract without this file learning about them. A factory must
// return a FRESH instance on every call, and every call must return the same concrete type — the
// round-trip and rejection cases below construct a second instance and expect it to be compatible
// with the first's bytes.
package sketchtest

import (
	"encoding/binary"
	"errors"
	"hash/crc32"
	"path/filepath"
	"reflect"
	"regexp"
	"testing"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/sketch"
	"github.com/stretchr/testify/require"
)

// RunSketchSuite is the conformance suite for the generic sketch.Sketch interface. name
// distinguishes multiple factories run in the same test binary; factory must return a fresh,
// ready-to-use Sketch on every call.
func RunSketchSuite(t *testing.T, name string, factory func(t *testing.T) sketch.Sketch) {
	t.Helper()

	t.Run(name+"/shape", func(t *testing.T) {
		s := factory(t)
		require.NotNil(t, s)

		// Header has no error return; any Header it produces, including the zero value, is
		// shape-valid.
		_ = s.Header()

		_, err := s.MarshalBinary()
		require.NoError(t, err, "a conforming sketch must be able to encode itself")
		require.ErrorIs(t, s.UnmarshalBinary(nil), sketch.ErrTruncated, errNilFrame)

		// Save and Load sit on the far side of the package boundary, and they are held to DIFFERENT
		// vocabularies — see requireLoadError and requireSaveError for why the two are not the same
		// contract. Each is probed twice: once on the path that succeeds, and once on a failure the
		// probe provokes deliberately, because a check that only ever sees nil is not a check.
		dir := t.TempDir()
		probe := filepath.Join(dir, "shape-probe.bin")
		requireSaveError(t, sketch.Save(probe, s))
		requireLoadError(t, sketch.Load(probe, factory(t)))

		refused := sketch.Save(filepath.Join(dir, sketch.TriedBloomBase), s)
		require.Error(t, refused, "§7.4: Save must refuse %s outright", sketch.TriedBloomBase)
		requireSaveError(t, refused)

		absent := sketch.Load(filepath.Join(dir, "never-written.bin"), factory(t))
		require.Error(t, absent, "loading a file that was never written must fail")
		requireLoadError(t, absent)
	})

	t.Run(name+"/behaviour", func(t *testing.T) {
		t.Run("frame_is_a_well_formed_qpks_header", func(t *testing.T) { runFrameShapeCase(t, factory) })
		t.Run("params_are_named_and_strictly_ascending", func(t *testing.T) { runParamOrderCase(t, factory) })
		t.Run("live_crc_is_zero_decoded_crc_is_not", func(t *testing.T) { runCRCVisibilityCase(t, factory) })
		t.Run("marshal_unmarshal_round_trip", func(t *testing.T) { runRoundTripCase(t, factory) })
		t.Run("crc_detects_a_flipped_bit", func(t *testing.T) { runFlippedBitRejectionCase(t, factory) })
		t.Run("malformed_frames_report_the_matching_sentinel", func(t *testing.T) { runRejectionCases(t, factory) })
		t.Run("nil_receiver_reports_rather_than_panics", func(t *testing.T) { runNilReceiverCase(t, factory) })
		t.Run("anything_marshalled_can_be_unmarshalled", func(t *testing.T) { runZeroValueCase(t, factory) })
	})
}

// errNilFrame explains what UnmarshalBinary(nil) must NOT be: silently accepted. It is spelled as a
// message rather than a comment because the assertion it decorates is the one a reader is most
// likely to mistake for a typo — a nil frame is not an empty sketch, it is a truncated one.
const errNilFrame = "UnmarshalBinary(nil) must be refused; a zero-length frame is truncated, " +
	"not an empty sketch"

// requireLoadError polices the READ side of the package boundary — sketch.Load and
// sketch.LoadWithLog, which talk to callers outside this package. It accepts nil and the three core
// sentinels ONLY, and this narrowness is the assertion, not an oversight.
//
// errors.go states the contract: Load maps every one of this package's decode sentinels to
// core.ErrNotFound before returning, because a sketch is a cache and an unreadable one and a
// missing one are the same event to everything upstream (§13 invariant 3). A Load that leaked a raw
// ErrCorrupt would force every caller to learn this package's private error vocabulary in order to
// answer "is my sketch usable?", which is exactly the coupling the mapping exists to prevent.
//
// It was once named requireKnownIOError and applied to Save as well, which stated something untrue
// of Save in a comment and never noticed, because the probe that used it only ever passed a nil.
func requireLoadError(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		return
	}
	known := errors.Is(err, core.ErrNotFound) ||
		errors.Is(err, core.ErrBudget) ||
		errors.Is(err, core.ErrDegraded)
	require.True(t, known,
		"Load must map every failure to a core sentinel at the package boundary "+
			"(§13 invariant 3); got %v", err)
}

// requireSaveError polices the WRITE side, and the vocabulary is deliberately a different one.
//
// Load's mapping exists because a missing sketch and an unreadable one are the same event to a
// caller: both mean "start empty". Nothing analogous is true of Save. A caller that could not write
// has to know WHY — §7.4's append-only refusal of sketches/tried.bloom is a routing error the caller
// fixes by calling ReplaceGenerational, while a marshal failure is a bug in the sketch it is holding
// — so Save reports core.ErrAppendOnly for the first and passes the encoder's own sentinel through
// for the second. Collapsing both to core.ErrNotFound would be actively misleading: nothing was
// looked up and nothing was missing.
//
// The filesystem's own errors are the third case and are NOT in the accepted set, because they are
// not this package's to name: paths.WriteAtomic's failures reach the caller as themselves. That is
// why this helper is applied to the two failures Save PRODUCES rather than to every failure Save can
// return.
func requireSaveError(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		return
	}
	known := errors.Is(err, core.ErrAppendOnly) ||
		errors.Is(err, sketch.ErrGenerational) ||
		errors.Is(err, sketch.ErrMalformed) ||
		errors.Is(err, sketch.ErrTooLarge)
	require.True(t, known,
		"a failure Save itself produces must be core.ErrAppendOnly or one of this package's "+
			"sentinels, never a bare error; got %v", err)
}

// The QPKS frame offsets this file needs (header.go's layout table). They are spelled out here
// rather than imported because they are unexported in package sketch, and they are named rather
// than inlined because getting any of them wrong would silently move a byte patch into a length
// field — where the decoder would answer ErrTruncated and the test would pass for the wrong reason.
const (
	// magicOffset is where the four-byte "QPKS" prefix begins.
	magicOffset = 0
	// verOffset is where the two-byte format version begins.
	verOffset = 4
	// kindOffset is the single Kind byte.
	kindOffset = 6
	// paramsBlockOffset is where the params block begins: immediately after the fixed 32-byte
	// prefix (magic, Ver, Kind, Reserved, Count, Created, ParamCount, BodyLen).
	paramsBlockOffset = 32
	// paramCountOffset is where the four-byte ParamCount begins.
	paramCountOffset = 24
	// paramNameLenSize is the width of a param entry's leading NameLen byte.
	paramNameLenSize = 1
	// paramValueSize is the width of a param entry's float64 value.
	paramValueSize = 8
	// crcSize is the width of the trailing CRC32C.
	crcSize = 4
)

// futureVersion is a format version above anything this build writes. It is FormatVersion + 1 so
// that the case keeps testing "a newer plugin's file" rather than a fixed number that would stop
// being in the future the day FormatVersion is bumped.
const futureVersion = sketch.FormatVersion + 1

// paramNameRE is the param-name alphabet header.go enforces on both sides of the wire. It is
// restated here rather than inferred, because the restriction is what forces bloom.go to write
// `fprate` while the config key stays `fpRate` — a decoder that quietly accepted the camel-cased
// name would make the two spellings interchangeable and the config key would drift into the file
// format.
var paramNameRE = regexp.MustCompile(`^[a-z0-9.]{1,32}$`)

// crcTable is the Castagnoli table every rebuilt frame below is re-checksummed with. The rejection
// cases have to recompute the CRC after patching a byte, because DecodeHeader verifies it and a
// stale checksum would make every one of them report ErrCorrupt regardless of what was patched.
var crcTable = crc32.MakeTable(crc32.Castagnoli)

// runFrameShapeCase asserts the four things a caller can learn from a frame without knowing which
// sketch produced it: it is non-empty, it decodes, it says QPKS, and it declares a version and a
// kind this build recognises. Everything else in this package is a body inside that envelope.
func runFrameShapeCase(t *testing.T, factory func(t *testing.T) sketch.Sketch) {
	t.Helper()

	frame, err := factory(t).MarshalBinary()
	require.NoError(t, err)
	require.NotEmpty(t, frame, "MarshalBinary must produce a non-empty encoding")

	h, body, err := sketch.DecodeHeader(frame)
	require.NoError(t, err, "a sketch's own frame must decode")
	require.Equal(t, sketch.HeaderMagic, h.Magic, "every frame must begin with QPKS")
	require.Equal(t, sketch.FormatVersion, h.Ver,
		"a frame written by this build must declare this build's FormatVersion")
	require.True(t, h.Kind.Valid(), "kind %v is not one of the five defined sketch kinds", h.Kind)
	require.Equal(t, len(frame)-paramsBlockOffset-paramsBlockLen(t, frame)-crcSize, len(body),
		"the decoded body must be exactly the bytes between the params block and the CRC")
}

// runParamOrderCase asserts the params block is canonical: every name is inside the [a-z0-9.]
// alphabet and no longer than 32 bytes, and the names are STRICTLY ascending.
//
// The order is what makes a given logical state have exactly one encoding. Go randomizes map
// iteration deliberately, so an encoder that emitted params in map order would produce different
// bytes for the same sketch on every run — and the frozen golden fixtures, the byte-identical
// re-marshal assertion and the content-addressed store all rest on that not happening.
func runParamOrderCase(t *testing.T, factory func(t *testing.T) sketch.Sketch) {
	t.Helper()

	frame, err := factory(t).MarshalBinary()
	require.NoError(t, err)

	names := frameParamNames(t, frame)
	require.NotEmpty(t, names,
		"every sketch in this package declares at least one param, so a frame with none is "+
			"self-describing about nothing")
	for i, n := range names {
		require.Regexp(t, paramNameRE, n, "param %d has a name outside the wire alphabet", i)
		if i > 0 {
			require.Less(t, names[i-1], n,
				"param names must be STRICTLY ascending; %q does not follow %q", n, names[i-1])
		}
	}
}

// runCRCVisibilityCase pins the one field a live sketch and a decoded one are required to DISAGREE
// about. sketch.Sketch documents Header().CRC32C as zero on a live sketch, because the checksum
// covers the encoded body and cannot be known without marshalling; DecodeHeader is the only thing
// that ever populates it. A caller comparing a live sketch's CRC32C against a file's is comparing
// zero against a real number, and this case is what keeps that documented rather than accidental.
func runCRCVisibilityCase(t *testing.T, factory func(t *testing.T) sketch.Sketch) {
	t.Helper()

	s := factory(t)
	require.Zero(t, s.Header().CRC32C, "a live sketch must report CRC32C == 0")

	frame, err := s.MarshalBinary()
	require.NoError(t, err)
	h, _, err := sketch.DecodeHeader(frame)
	require.NoError(t, err)
	require.NotZero(t, h.CRC32C, "a decoded frame must report the checksum it carried")
	require.Equal(t, binary.LittleEndian.Uint32(frame[len(frame)-crcSize:]), h.CRC32C,
		"the reported CRC32C must be the four bytes actually on the wire")
}

// runRoundTripCase asserts that marshalling a sketch, decoding it into a SECOND freshly constructed
// instance and re-marshalling that reproduces the original bytes exactly.
//
// Byte equality rather than header equality is the assertion. Two encodings of the same logical
// state would both decode correctly and would still break the content-addressed store (SP-06),
// which keys on the bytes. The intermediate is a fresh instance rather than the original so that a
// decoder which left a field untouched — inheriting it from whatever the receiver already held — is
// caught here instead of passing because the receiver happened to hold the right value.
func runRoundTripCase(t *testing.T, factory func(t *testing.T) sketch.Sketch) {
	t.Helper()
	s1 := factory(t)
	data, err := s1.MarshalBinary()
	require.NoError(t, err)
	require.NotEmpty(t, data, "fixture sanity: MarshalBinary must produce a non-empty encoding")

	s2 := factory(t)
	require.NoError(t, s2.UnmarshalBinary(data))
	require.Equal(t, s1.Header(), s2.Header(),
		"UnmarshalBinary(MarshalBinary(s)) must reproduce an equal Header")

	again, err := s2.MarshalBinary()
	require.NoError(t, err)
	require.Equal(t, data, again,
		"unmarshalling into a fresh instance and re-marshalling must be byte-identical")
}

// runFlippedBitRejectionCase asserts the CRC32C catches bit rot: a frame with one byte changed must
// be refused, not decoded into a plausible half-sketch (00-ARCHITECTURE.md §5.7, "CRC + version
// checked").
//
// It works entirely in memory, on the bytes MarshalBinary returned. Routing it through Save and
// Load would test the file plumbing as well as the checksum, and in-memory is also the SHARPER
// assertion: it pins the exact sentinel, ErrCorrupt, rather than the core.ErrNotFound the I/O layer
// maps every decode failure to. The filesystem half of the contract — that a corrupt FILE reports
// both core.ErrNotFound and ErrCorrupt — belongs to io_test.go's TestLoad_CorruptIsNotFoundAndCorrupt.
//
// The flipped byte is the first byte of the FIRST param's 8-byte value. That placement is
// deliberate. DecodeHeader verifies the CRC only after every structural check has passed, so the
// flip must leave the structure intact for ErrCorrupt to be the only possible answer: a flip in
// ParamCount, BodyLen or a NameLen would trip ErrTruncated or ErrMalformed first, and the test would
// then pass without ever exercising the checksum.
func runFlippedBitRejectionCase(t *testing.T, factory func(t *testing.T) sketch.Sketch) {
	t.Helper()

	frame, err := factory(t).MarshalBinary()
	require.NoError(t, err)
	require.NotEmpty(t, frame, "fixture sanity: MarshalBinary must produce a non-empty encoding")

	// The pristine frame must decode, so that ErrCorrupt below is attributable to the flip and to
	// nothing else about this sketch's encoding.
	require.NoError(t, factory(t).UnmarshalBinary(frame))

	i := firstParamValueByte(t, frame)
	t.Logf("flipping byte %d of %d: inside the first param's value, clear of ParamCount [24,28), "+
		"BodyLen [28,32), the NameLen byte at %d and the trailing CRC32C",
		i, len(frame), paramsBlockOffset)

	corrupted := append([]byte(nil), frame...)
	corrupted[i] ^= 0xFF

	err = factory(t).UnmarshalBinary(corrupted)
	require.Error(t, err, "a frame with a flipped bit must be rejected, not decoded")
	require.ErrorIs(t, err, sketch.ErrCorrupt,
		"§5.7: a CRC32C mismatch is the bit-rot signal and must report ErrCorrupt (byte %d of %d)",
		i, len(frame))
}

// runRejectionCases walks the six structural rejections every decoder in this package inherits from
// the QPKS envelope — two truncations, a wrong magic, a wrong kind, and a version on each side of
// the supported one — and pins the exact sentinel each must answer with.
//
// Every patched frame is RE-CHECKSUMMED. Without that, all four would report ErrCorrupt — the
// checksum would be the reason each frame was refused, and the four checks the cases exist to
// exercise would never run. The two truncations are the exception: a truncated frame has no correct
// checksum to restore, which is precisely the interrupted-write signal ErrTruncated names.
func runRejectionCases(t *testing.T, factory func(t *testing.T) sketch.Sketch) {
	t.Helper()

	frame, err := factory(t).MarshalBinary()
	require.NoError(t, err)

	cases := []struct {
		name  string
		build func() []byte
		want  error
	}{
		{
			// Shorter than the 36-byte minimum frame: there is not even a length block to read.
			name:  "shorter_than_the_minimum_frame",
			build: func() []byte { return append([]byte(nil), frame[:crcSize]...) },
			want:  sketch.ErrTruncated,
		},
		{
			// One byte short: every declared length is intact and none of them adds up.
			name:  "one_byte_short",
			build: func() []byte { return append([]byte(nil), frame[:len(frame)-1]...) },
			want:  sketch.ErrTruncated,
		},
		{
			name: "wrong_magic",
			build: func() []byte {
				out := append([]byte(nil), frame...)
				out[magicOffset] = 'X'
				return restamp(out)
			},
			want: sketch.ErrBadMagic,
		},
		{
			name: "wrong_kind",
			build: func() []byte {
				out := append([]byte(nil), frame...)
				out[kindOffset] = byte(otherKind(sketch.Kind(frame[kindOffset])))
				return restamp(out)
			},
			want: sketch.ErrKindMismatch,
		},
		{
			name: "version_zero",
			build: func() []byte {
				out := append([]byte(nil), frame...)
				binary.LittleEndian.PutUint16(out[verOffset:], 0)
				return restamp(out)
			},
			want: sketch.ErrUnsupportedVersion,
		},
		{
			name: "version_from_the_future",
			build: func() []byte {
				out := append([]byte(nil), frame...)
				binary.LittleEndian.PutUint16(out[verOffset:], futureVersion)
				return restamp(out)
			},
			want: sketch.ErrUnsupportedVersion,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// factory(t) is a fresh instance, so a decoder that half-applied the frame before
			// rejecting it leaves the damage visible to the assertions rather than to the next case.
			err := factory(t).UnmarshalBinary(tc.build())
			require.Error(t, err, "a %s frame must be refused, not decoded", tc.name)
			require.ErrorIs(t, err, tc.want)
		})
	}
}

// runNilReceiverCase pins the plan's uniform rule: MarshalBinary and UnmarshalBinary on a NIL
// receiver report ErrMalformed rather than panicking. §12.3 is the reason — a hook that dies takes
// observability down with it — and a nil sketch is not hypothetical: it is what a composition root
// holds after a constructor it forgot to check, and what a map lookup returns for a session that
// was never registered.
//
// The nil is built by reflection rather than by a type switch so that a Sketch this package has
// never heard of is still covered. Any implementation is a pointer type (the interface's
// UnmarshalBinary mutates), so reflect.Zero of the factory's dynamic type is exactly the typed nil
// a caller would hold.
func runNilReceiverCase(t *testing.T, factory func(t *testing.T) sketch.Sketch) {
	t.Helper()

	dyn := reflect.TypeOf(factory(t))
	require.Equal(t, reflect.Pointer, dyn.Kind(),
		"a Sketch must be a pointer type: UnmarshalBinary replaces the receiver's state")

	nilRecv, ok := reflect.Zero(dyn).Interface().(sketch.Sketch)
	require.True(t, ok, "the typed nil of %v must still satisfy sketch.Sketch", dyn)

	_, err := nilRecv.MarshalBinary()
	require.ErrorIs(t, err, sketch.ErrMalformed,
		"MarshalBinary on a nil %v must report ErrMalformed, not panic", dyn)
	require.ErrorIs(t, nilRecv.UnmarshalBinary(nil), sketch.ErrMalformed,
		"UnmarshalBinary on a nil %v must report ErrMalformed, not panic", dyn)
}

// runZeroValueCase pins the ceiling rule's second half (sketch's errors.go): every sketch that can
// be MARSHALLED must be re-readable. A frame this build's own decoder is guaranteed to reject is
// worse than no frame, because the refusal is loud and recoverable at the call site whereas the file
// is discovered dead on the next restart — the one failure §6.2 exists to prevent and the one
// sketch's doc.go says cannot happen.
//
// The receiver is the type's ZERO value, because that is the state the rule is actually reachable
// through: a caller outside the package writing `var c sketch.CMS` rather than calling NewCMS gets a
// sketch with no dimensions, and Save takes the interface. Two of the five refused such a receiver
// from the start; the other two wrote a valid 94- and 54-byte frame that their own decoders then
// refused forever, which is what put this case in the SHARED suite rather than in two more per-type
// assertions.
//
// The assertion is deliberately a DISJUNCTION rather than "an unsized sketch must be refused",
// because a legal zero value exists: sketch.SigSketch's is the disabled signature, Perms 0 and no
// minima, which round-trips exactly and must keep doing so. What no implementation may do is the
// third thing — write bytes and then refuse them.
func runZeroValueCase(t *testing.T, factory func(t *testing.T) sketch.Sketch) {
	t.Helper()

	dyn := reflect.TypeOf(factory(t))
	require.Equal(t, reflect.Pointer, dyn.Kind(),
		"a Sketch must be a pointer type: UnmarshalBinary replaces the receiver's state")
	zero, ok := reflect.New(dyn.Elem()).Interface().(sketch.Sketch)
	require.True(t, ok, "a freshly allocated %v must still satisfy sketch.Sketch", dyn)

	frame, err := zero.MarshalBinary()
	if err != nil {
		require.ErrorIs(t, err, sketch.ErrMalformed,
			"a zero %v that refuses to marshal must say so with ErrMalformed", dyn)
		require.Nil(t, frame, "nothing may be written when nothing can be read back")
		return
	}

	require.NoError(t, factory(t).UnmarshalBinary(frame),
		"a zero %v marshalled to %d bytes that its own decoder then refuses: either refuse to "+
			"marshal it, or decode what was written — never both", dyn, len(frame))
}

// restamp recomputes the trailing CRC32C over a patched frame, so that the patched field is the only
// thing the decoder can object to.
func restamp(frame []byte) []byte {
	crc := crc32.Checksum(frame[:len(frame)-crcSize], crcTable)
	binary.LittleEndian.PutUint32(frame[len(frame)-crcSize:], crc)
	return frame
}

// otherKind returns a valid sketch.Kind that is not k, so the wrong-kind case tests the kind CHECK
// rather than the kind-validity check that precedes it. Feeding an invalid kind byte instead would
// be refused by DecodeHeader as ErrMalformed and would never reach the receiver's own comparison.
func otherKind(k sketch.Kind) sketch.Kind {
	if k == sketch.KindBloom {
		return sketch.KindCMS
	}
	return sketch.KindBloom
}

// frameParamNames walks a frame's params block and returns the names in wire order. It parses the
// bytes rather than reading DecodeHeader's map because ORDER is the thing under test, and a Go map
// has none.
func frameParamNames(t *testing.T, frame []byte) []string {
	t.Helper()
	require.Greater(t, len(frame), paramsBlockOffset+crcSize,
		"fixture sanity: the frame is shorter than the fixed prefix and CRC")

	count := int(binary.LittleEndian.Uint32(frame[paramCountOffset : paramCountOffset+crcSize]))
	names := make([]string, 0, count)
	off := paramsBlockOffset
	for i := 0; i < count; i++ {
		require.Less(t, off, len(frame)-crcSize, "param %d begins past the end of the frame", i)
		nameLen := int(frame[off])
		require.LessOrEqual(t, off+paramNameLenSize+nameLen+paramValueSize, len(frame)-crcSize,
			"param %d runs past the params block", i)
		names = append(names, string(frame[off+paramNameLenSize:off+paramNameLenSize+nameLen]))
		off += paramNameLenSize + nameLen + paramValueSize
	}
	return names
}

// paramsBlockLen returns the encoded size of a frame's params block, so runFrameShapeCase can state
// the body length as an arithmetic identity over the frame rather than as a number it was told.
func paramsBlockLen(t *testing.T, frame []byte) int {
	t.Helper()
	total := 0
	for _, n := range frameParamNames(t, frame) {
		total += paramNameLenSize + len(n) + paramValueSize
	}
	return total
}

// firstParamValueByte returns the index of the first byte of the first param's float64 value, and
// proves on the way that the index really does land inside the params block: after the fixed prefix,
// after that param's NameLen byte and name, and before the trailing CRC32C.
func firstParamValueByte(t *testing.T, frame []byte) int {
	t.Helper()
	require.Greater(t, len(frame), paramsBlockOffset,
		"fixture sanity: the frame is shorter than the fixed prefix")

	nameLen := int(frame[paramsBlockOffset])
	require.Positive(t, nameLen,
		"fixture sanity: every sketch in this package declares at least one param")

	i := paramsBlockOffset + paramNameLenSize + nameLen
	require.Greater(t, i, paramsBlockOffset,
		"the flip must land past the fixed prefix, clear of ParamCount and BodyLen")
	require.LessOrEqual(t, i+paramValueSize, len(frame)-crcSize,
		"the flip must land inside a param value, clear of the body and the trailing CRC")
	return i
}

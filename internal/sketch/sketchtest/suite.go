// Package sketchtest is the conformance suite for the sketch package (00-ARCHITECTURE.md §5.22):
// every implementation SP-03 ships must pass RunSketchSuite, plus the per-sketch-type suites below
// (RunBloomSuite, RunCMSSuite, RunHLLSuite, RunMisraGriesSuite, RunMinHashSuite) that exercise the
// accuracy guarantee specific to each algorithm. SP-01 ships every suite, including the behaviour
// assertions SP-03 inherits (Rule W-1) — only each guarded /behaviour block is skipped until a
// real implementation lands.
package sketchtest

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/sketch"
	"github.com/stretchr/testify/require"
)

// ruleW1SkipMsg is the exact, mandatory Rule W-1 skip reason (00-ARCHITECTURE.md §5.22; §15 of
// plans/V1-SP-01-foundation-toolchain-and-contracts.md). devtool lint's stubskips sub-check greps
// test output for this literal string, so it must never be paraphrased.
const ruleW1SkipMsg = "behaviour: implementation is a stub (Rule W-1)"

// RunSketchSuite is the conformance suite for the generic sketch.Sketch interface. name
// distinguishes multiple factories run in the same test binary; factory must return a fresh,
// ready-to-use Sketch on every call, and every call must return the SAME concrete type (the
// round-trip and CRC-rejection cases below construct a second instance via factory and expect it
// to be compatible with the first's encoded bytes).
func RunSketchSuite(t *testing.T, name string, factory func(t *testing.T) sketch.Sketch) {
	t.Helper()

	t.Run(name+"/shape", func(t *testing.T) {
		s := factory(t)
		require.NotNil(t, s)

		// Header has no error return; any Header it produces, including the zero value, is
		// shape-valid.
		_ = s.Header()

		_, err := s.MarshalBinary()
		requireKnownError(t, err)
		requireKnownError(t, s.UnmarshalBinary(nil))

		// Save and Load are held to the NARROWER vocabulary. They sit on the far side of the
		// package boundary, where the mapping to core.ErrNotFound is the contract.
		dir := t.TempDir()
		requireKnownIOError(t, sketch.Save(filepath.Join(dir, "shape-probe.bin"), s))
		requireKnownIOError(t, sketch.Load(filepath.Join(dir, "shape-probe.bin"), s))
	})

	if skipIfStubSketch(t, factory(t)) {
		return
	}

	t.Run(name+"/behaviour", func(t *testing.T) {
		t.Run("marshal_unmarshal_round_trip", func(t *testing.T) { runRoundTripCase(t, factory) })
		t.Run("crc_detects_a_flipped_bit", func(t *testing.T) { runFlippedBitRejectionCase(t, factory) })
	})
}

// requireKnownError polices the CODEC boundary — MarshalBinary and UnmarshalBinary, which talk
// about bytes. It fails the test unless err is nil or wraps one of two families of sentinel, and
// the two exist because two different layers can be speaking.
//
// The four core sentinels (00-ARCHITECTURE.md §5.22; §15 of
// plans/V1-SP-01-foundation-toolchain-and-contracts.md) are what a STUB reports: a codec that has
// not been written yet answers core.ErrNotImplemented.
//
// The nine sketch sentinels (errors.go) are what a REAL codec reports about bytes. Once SP-03
// lands, UnmarshalBinary(nil) answers ErrTruncated — a precise, correct description of a
// zero-length frame, not an unexpected failure. Refusing it here would hold every real
// implementation to a stub's error vocabulary and make the shape block fail for behaving
// correctly, which is the opposite of what a conformance suite is for.
//
// Both families are accepted rather than one being swapped for the other, because this suite runs
// against stubs and real implementations in the same test binary for as long as any sketch type
// remains unimplemented.
//
// It is deliberately NOT used for Save and Load — see requireKnownIOError, which is the whole
// reason the two helpers are separate.
func requireKnownError(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		return
	}
	known := errors.Is(err, core.ErrNotImplemented) ||
		errors.Is(err, core.ErrNotFound) ||
		errors.Is(err, core.ErrBudget) ||
		errors.Is(err, core.ErrDegraded) ||
		errors.Is(err, sketch.ErrBadMagic) ||
		errors.Is(err, sketch.ErrUnsupportedVersion) ||
		errors.Is(err, sketch.ErrKindMismatch) ||
		errors.Is(err, sketch.ErrCorrupt) ||
		errors.Is(err, sketch.ErrTruncated) ||
		errors.Is(err, sketch.ErrMalformed) ||
		errors.Is(err, sketch.ErrShapeMismatch) ||
		errors.Is(err, sketch.ErrTooLarge) ||
		errors.Is(err, sketch.ErrGenerational)
	require.True(t, known, "unexpected error: %v", err)
}

// requireKnownIOError polices the PACKAGE boundary — sketch.Save and sketch.Load, which talk to
// callers outside this package. It accepts nil and the four core sentinels ONLY, and this
// narrowness is the assertion, not an oversight.
//
// errors.go states the contract: Load maps every one of this package's decode sentinels to
// core.ErrNotFound before returning, because a sketch is a cache and an unreadable one and a
// missing one are the same event to everything upstream (§13 invariant 3). A Load that leaked a
// raw ErrCorrupt would force every caller to learn this package's private error vocabulary in
// order to answer "is my sketch usable?", which is exactly the coupling the mapping exists to
// prevent — and a helper that accepted both families here would let that regression through
// silently.
//
// The two vocabularies therefore differ on purpose: a codec is allowed to be specific about bytes,
// an I/O entry point is required to be generic about caches.
func requireKnownIOError(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		return
	}
	known := errors.Is(err, core.ErrNotImplemented) ||
		errors.Is(err, core.ErrNotFound) ||
		errors.Is(err, core.ErrBudget) ||
		errors.Is(err, core.ErrDegraded)
	require.True(t, known,
		"Save/Load must map every failure to a core sentinel at the package boundary "+
			"(§13 invariant 3); got %v", err)
}

// isStubSketch reports whether s is still a stub, using MarshalBinary as the probe
// (plans/OWNERS.tsv: sketch's probe method is MarshalBinary). MarshalBinary has an error return,
// so — unlike chunk, symbols, redact and grammar's suites — this can check core.IsNotImplemented
// directly rather than relying on an inferred zero-value heuristic.
func isStubSketch(t *testing.T, s sketch.Sketch) bool {
	t.Helper()
	_, err := s.MarshalBinary()
	return core.IsNotImplemented(err)
}

// skipIfStubSketch calls t.Skip with the exact Rule W-1 message when s is still a stub, and
// reports whether it did. It is shared by every suite in this package (RunSketchSuite and every
// per-type suite below), since Bloom, CMS, HLL and MisraGries all implement sketch.Sketch and so
// all share the same MarshalBinary probe.
func skipIfStubSketch(t *testing.T, s sketch.Sketch) bool {
	t.Helper()
	if isStubSketch(t, s) {
		t.Skip(ruleW1SkipMsg)
		return true
	}
	return false
}

// runRoundTripCase asserts that marshaling one Sketch and unmarshaling the result into a second,
// freshly constructed instance reproduces an equal Header.
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
}

// The two QPKS frame offsets this case needs (header.go's layout table). They are spelled out here
// rather than imported because they are unexported in package sketch, and they are named rather
// than inlined because getting either wrong would silently move the flip into a length field —
// where the decoder would answer ErrTruncated and the test would pass for the wrong reason.
const (
	// paramsBlockOffset is where the params block begins: immediately after the fixed 32-byte
	// prefix (magic, Ver, Kind, Reserved, Count, Created, ParamCount, BodyLen).
	paramsBlockOffset = 32
	// paramNameLenSize is the width of a param entry's leading NameLen byte.
	paramNameLenSize = 1
	// paramValueSize is the width of a param entry's float64 value.
	paramValueSize = 8
	// crcSize is the width of the trailing CRC32C.
	crcSize = 4
)

// runFlippedBitRejectionCase asserts the CRC32C catches bit rot: a frame with one byte changed
// must be refused, not decoded into a plausible half-sketch (00-ARCHITECTURE.md §5.7, "CRC +
// version checked").
//
// It works entirely in memory, on the bytes MarshalBinary returned. Routing it through Save and
// Load would test the file plumbing as well as the checksum, and would make this suite depend on
// io.go — which lands several commits after the first real MarshalBinary, so the case would fail
// for a reason that has nothing to do with checksums. In-memory is also the SHARPER assertion: it
// pins the exact sentinel, ErrCorrupt, rather than the core.ErrNotFound the I/O layer maps every
// decode failure to. The filesystem half of the contract — that a corrupt FILE reports both
// core.ErrNotFound and ErrCorrupt — belongs to io_test.go's TestLoad_CorruptIsNotFoundAndCorrupt.
//
// The flipped byte is the first byte of the FIRST param's 8-byte value. That placement is
// deliberate. DecodeHeader verifies the CRC only after every structural check has passed, so the
// flip must leave the structure intact for ErrCorrupt to be the only possible answer: a flip in
// ParamCount, BodyLen or a NameLen would trip ErrTruncated or ErrMalformed first, and the test
// would then pass without ever exercising the checksum. A param value is the safest such target
// for every sketch in this package, since each one declares at least one param but a degenerate
// body may be empty.
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

// firstParamValueByte returns the index of the first byte of the first param's float64 value, and
// proves on the way that the index really does land inside the params block: after the fixed
// prefix, after that param's NameLen byte and name, and before the trailing CRC32C.
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

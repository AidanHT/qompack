package sketch

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// The five fuzz targets of this package, one per decoder that reads bytes off disk or out of a
// store record. They are named per type rather than as a single FuzzUnmarshalBinary deliberately:
// each receiver has its own param validation, its own body layout and its own allocation-sizing
// hazard, and one target sharing one corpus would spend its whole budget re-deriving the QPKS
// prefix all five share instead of reaching any one of the five bodies.
//
// Every target asserts the same three things:
//
//   - No panic. §12.3: a hook that dies takes observability with it, and these inputs are not
//     hypothetical — bit rot and an interrupted write produce exactly these buffers.
//   - On success, the value round-trips: MarshalBinary then UnmarshalBinary reproduces an equal
//     sketch, and the second encoding is byte-identical to the first. A frame this package accepts
//     but cannot write back is a sketch that will not survive the next restart, which is the one
//     failure §6.2 exists to prevent.
//   - On failure, the error is one of the nine package sentinels AND the receiver is still usable.
//     A decoder that half-applied a forged frame before rejecting it would leave the daemon holding
//     a sketch whose declared dimensions disagree with its backing array, and the next Add would be
//     an out-of-range write rather than a wrong answer.
//
// Nightly CI runs 10 minutes per target (00-ARCHITECTURE.md §8, nightly.yml).

// fuzzCorpusRoot is testdata/corpora/sketch/, the committed seed corpus this subplan owns. It
// mirrors the shape of testdata/corpora/hookio/: one directory per target, raw payload bytes in
// each file, no wrapper format. Seeds live on disk rather than only in f.Add so that the nightly
// job and a local `go test -fuzz` start from the same inputs, and so that a seed can be read with a
// hex dump when it is the thing that failed.
const fuzzCorpusRoot = "../../testdata/corpora/sketch"

// fuzzSentinels is the complete set of errors any decoder in this package may report. Asserting
// membership rather than merely `err != nil` is what makes the set a contract: callers match with
// errors.Is, so a decoder answering anything outside this list is reporting a failure nobody
// downstream has been told how to handle.
func fuzzSentinels() []error {
	return []error{
		ErrBadMagic,
		ErrUnsupportedVersion,
		ErrKindMismatch,
		ErrCorrupt,
		ErrTruncated,
		ErrMalformed,
		ErrShapeMismatch,
		ErrTooLarge,
		ErrGenerational,
	}
}

// isSketchSentinel reports whether err wraps one of this package's declared sentinels.
func isSketchSentinel(err error) bool {
	for _, s := range fuzzSentinels() {
		if errors.Is(err, s) {
			return true
		}
	}
	return false
}

// fuzzRequireSentinel fails unless err is one of the nine.
func fuzzRequireSentinel(t *testing.T, err error) {
	t.Helper()
	require.True(t, isSketchSentinel(err),
		"a decoder rejected a frame with an error outside the package's sentinel set: %v", err)
}

// fuzzAddCorpus seeds f with every file in testdata/corpora/sketch/<target>/.
//
// A missing directory is a hard failure rather than a silent skip. These seeds are the reason the
// targets reach past the magic check at all — a corpus that quietly vanished would leave the
// nightly job fuzzing four bytes of prefix for ten minutes and reporting success.
func fuzzAddCorpus(f *testing.F, target string) {
	f.Helper()
	dir := filepath.Join(fuzzCorpusRoot, target)
	entries, err := os.ReadDir(dir)
	require.NoError(f, err, "seed corpus %s is missing", dir)
	require.NotEmpty(f, entries, "seed corpus %s is empty", dir)
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		b, readErr := os.ReadFile(filepath.Join(dir, e.Name()))
		require.NoError(f, readErr, "reading seed %s", e.Name())
		f.Add(b)
	}
}

// fuzzUniversalSeeds are the inputs every target gets on top of its own corpus: the boundary
// buffers that exercise the shared QPKS prefix rather than any one body. They are inline rather
// than files because they carry no structure worth inspecting.
func fuzzUniversalSeeds(f *testing.F) {
	f.Helper()
	f.Add([]byte(nil))
	f.Add([]byte{})
	f.Add([]byte("QPKS"))
	f.Add(make([]byte, minFrameLen))
}

// fuzzRestamp returns data with the QPKS magic, FormatVersion and kind stamped over its prefix and
// a CORRECT CRC32C recomputed over the result, or nil if data is too short to be a frame at all.
//
// Without it these targets would be fuzzing four bytes of magic and a checksum. A mutation engine
// cannot produce a valid CRC32C by chance, so every mutated frame is refused at the last check of
// pass 1 and the param walk, the length arithmetic and the five body decoders behind it are never
// reached — the entire surface the ceilings in errors.go exist to defend. Restamping leaves
// ParamCount, BodyLen, every param name and value, and the whole body under the fuzzer's control
// and hands the decoder a frame that is self-consistent enough to be worth rejecting for a reason
// that is actually about the sketch.
//
// It is also the accurate threat model. §6.2 makes these files permanent memory read back by a
// build that may not be the one that wrote them, so "a structurally valid frame whose params are
// wrong" is a real input, where "a frame with a broken checksum" is only ever bit rot.
func fuzzRestamp(data []byte, kind Kind) []byte {
	if len(data) < minFrameLen {
		return nil
	}
	out := append([]byte(nil), data...)
	copy(out[0:magicLen], HeaderMagic[:])
	binary.LittleEndian.PutUint16(out[offVer:offKind], FormatVersion)
	out[offKind] = byte(kind)
	return fuzzFixCRC(out)
}

// fuzzFixCRC rewrites frame's trailing CRC32C in place and returns it, so that the checksum stops
// being the reason a patched frame is refused. TestProp_UnmarshalNeverPanics uses it too: it is the
// difference between a mutation that dies at the last check of pass 1 and one that reaches the body.
func fuzzFixCRC(frame []byte) []byte {
	crc := crc32.Checksum(frame[:len(frame)-crcLen], crc32.MakeTable(crc32.Castagnoli))
	binary.LittleEndian.PutUint32(frame[len(frame)-crcLen:], crc)
	return frame
}

// fuzzCase is one target's receiver, its zero value and its liveness probe.
type fuzzCase struct {
	// name identifies the receiver in failure messages.
	name string
	// receiver builds the instance the fuzzed bytes are decoded into, at the SMALLEST legal
	// dimensions. Small is deliberate: a decoder that ignored a declared dimension and kept the
	// receiver's own would produce a visibly wrong sketch here, where a receiver built at the
	// corpus's dimensions would hide that behind a coincidence.
	receiver func() Sketch
	// zero builds the zero value the re-encoded frame is decoded into, so that a field a decoder
	// forgot to assign shows up as a mismatch rather than being inherited from a constructor.
	zero func() Sketch
	// probe exercises the sketch after the decode attempt. It is the "still usable" half of the
	// contract: it must not panic, and what it asserts must hold for every state the decoder can
	// legally leave behind.
	probe func(t *testing.T, s Sketch)
}

// fuzzDecode runs one target's whole contract against one input.
func fuzzDecode(t *testing.T, c fuzzCase, data []byte) {
	t.Helper()

	s := c.receiver()
	if err := s.UnmarshalBinary(data); err != nil {
		fuzzRequireSentinel(t, err)
		c.probe(t, s)
		return
	}

	frame, err := s.MarshalBinary()
	require.NoError(t, err, "%s accepted a frame it cannot re-encode", c.name)
	require.NotEmpty(t, frame, "%s re-encoded to nothing", c.name)

	round := c.zero()
	require.NoError(t, round.UnmarshalBinary(frame),
		"%s wrote a frame its own decoder refuses", c.name)
	require.Equal(t, s, round, "%s: marshal ∘ unmarshal must reproduce an equal value", c.name)

	again, err := round.MarshalBinary()
	require.NoError(t, err)
	require.True(t, bytes.Equal(frame, again),
		"%s: the re-encoding must be byte-identical", c.name)

	c.probe(t, s)
}

// fuzzRun applies fuzzDecode to the input verbatim and then to its restamped form. Both passes
// matter: the verbatim one is the real Load path, including the magic, version, kind and checksum
// rejections; the restamped one is the only way a mutation engine ever reaches a body decoder.
func fuzzRun(t *testing.T, c fuzzCase, kind Kind, data []byte) {
	t.Helper()
	fuzzDecode(t, c, data)
	if restamped := fuzzRestamp(data, kind); restamped != nil {
		fuzzDecode(t, c, restamped)
	}
}

// The dimensions each target's receiver is constructed with — the smallest legal ones, for the
// reason fuzzCase.receiver gives.
const (
	// fuzzBloomCapacity and fuzzBloomFPRate size the Bloom receiver.
	fuzzBloomCapacity = 1
	fuzzBloomFPRate   = 0.5
	// fuzzCMSEpsilon and fuzzCMSDelta size the Count-Min receiver at its coarsest legal shape.
	fuzzCMSEpsilon = 0.5
	fuzzCMSDelta   = 0.5
	// fuzzMGCounters sizes the Misra-Gries receiver at one counter.
	fuzzMGCounters = 1
)

// fuzzProbeKey is the key every liveness probe adds. Its content is irrelevant; what matters is
// that the operation runs on whatever state the decoder left behind.
const fuzzProbeKey = "probe"

// FuzzBloomUnmarshalBinary fuzzes Bloom.UnmarshalBinary — the decoder behind tried.bloom, the one
// sketch file §7.4 makes append-only and therefore the one most likely to be read back after an
// interrupted write. Its threat model is the m param: it is the length of the make() that allocates
// the bit array, so a frame claiming m = 1e18 has to be refused before it becomes an out-of-memory
// kill of the daemon.
func FuzzBloomUnmarshalBinary(f *testing.F) {
	fuzzAddCorpus(f, "bloom")
	fuzzUniversalSeeds(f)

	c := fuzzCase{
		name:     "Bloom",
		receiver: func() Sketch { return NewBloom(fuzzBloomCapacity, fuzzBloomFPRate) },
		zero:     func() Sketch { return new(Bloom) },
		probe: func(t *testing.T, s Sketch) {
			b, ok := s.(*Bloom)
			require.True(t, ok)
			b.Add([]byte(fuzzProbeKey))
			require.True(t, b.Test([]byte(fuzzProbeKey)),
				"the filter must still record an Add after a decode attempt")
		},
	}
	f.Fuzz(func(t *testing.T, data []byte) { fuzzRun(t, c, KindBloom, data) })
}

// FuzzCMSUnmarshalBinary fuzzes CMS.UnmarshalBinary. Its threat model is the width×depth product:
// both are header params and both are multiplied into the make() that allocates the counter table,
// so a forged pair is the shortest path from a corrupt file to an out-of-memory kill.
func FuzzCMSUnmarshalBinary(f *testing.F) {
	fuzzAddCorpus(f, "cms")
	fuzzUniversalSeeds(f)

	c := fuzzCase{
		name:     "CMS",
		receiver: func() Sketch { return NewCMS(fuzzCMSEpsilon, fuzzCMSDelta) },
		zero:     func() Sketch { return new(CMS) },
		probe: func(t *testing.T, s Sketch) {
			cms, ok := s.(*CMS)
			require.True(t, ok)
			cms.Add([]byte(fuzzProbeKey), 1)
			require.GreaterOrEqual(t, cms.Estimate([]byte(fuzzProbeKey)), uint32(1),
				"the table must still record an Add after a decode attempt")
		},
	}
	f.Fuzz(func(t *testing.T, data []byte) { fuzzRun(t, c, KindCMS, data) })
}

// FuzzHLLUnmarshalBinary fuzzes HLL.UnmarshalBinary. Beyond the register count, the register VALUES
// are part of the threat model: a body of 0xFF bytes drives Cardinality's harmonic mean past 2^277,
// and converting an out-of-range float64 to uint64 is implementation-defined in Go — the same file
// would report different cardinalities on amd64 and arm64. Cardinality is therefore called on
// whatever register array survives, not merely inspected.
func FuzzHLLUnmarshalBinary(f *testing.F) {
	fuzzAddCorpus(f, "hll")
	fuzzUniversalSeeds(f)

	c := fuzzCase{
		name:     "HLL",
		receiver: func() Sketch { return NewHLL(MinHLLRegisters) },
		zero:     func() Sketch { return new(HLL) },
		probe: func(t *testing.T, s Sketch) {
			h, ok := s.(*HLL)
			require.True(t, ok)
			require.LessOrEqual(t, h.Cardinality(), uint64(maxTotalFloat),
				"a decoded register array must not drive the estimator past its clamp")
			h.Add([]byte(fuzzProbeKey))
			require.Positive(t, h.Cardinality(),
				"the sketch must still record an Add after a decode attempt")
		},
	}
	f.Fuzz(func(t *testing.T, data []byte) { fuzzRun(t, c, KindHLL, data) })
}

// FuzzMisraGriesUnmarshalBinary fuzzes MisraGries.UnmarshalBinary — the only decoder here whose body
// is variable-length and self-describing, and therefore the only one where a declared length can
// point past the buffer. EntryCount, every KeyLen and every count are attacker-controlled, and the
// strictly-ascending key order is a structural claim a forged frame can violate without any length
// disagreeing with any other.
func FuzzMisraGriesUnmarshalBinary(f *testing.F) {
	fuzzAddCorpus(f, "mg")
	fuzzUniversalSeeds(f)

	c := fuzzCase{
		name:     "MisraGries",
		receiver: func() Sketch { return NewMisraGries(fuzzMGCounters) },
		zero:     func() Sketch { return new(MisraGries) },
		probe: func(t *testing.T, s Sketch) {
			m, ok := s.(*MisraGries)
			require.True(t, ok)
			before := m.Total()
			// Add is the sharp end of "still usable": the counter map came out of the body, and a
			// decoder that left it nil would panic on this assignment rather than in production.
			// What Top then reports is NOT asserted to be non-empty — a full table of ones can
			// legally be emptied by one more Add, which is the decrement phase working.
			m.Add(fuzzProbeKey, 1)
			require.Greater(t, m.Total(), before, "an Add must move the stream total")
			require.LessOrEqual(t, len(m.Top(0)), m.K(),
				"the summary must never hold more than k counters")
		},
	}
	f.Fuzz(func(t *testing.T, data []byte) { fuzzRun(t, c, KindMisraGries, data) })
}

// FuzzSignatureUnmarshalBinary fuzzes Signature.UnmarshalBinary — the compact, unframed form that
// travels in a store record field (SP-06). It carries no CRC of its own, because the record line
// around it is checksummed, so this decoder's entire defence is the exact-length check against
// 2 + 8×Perms and the MaxPermutations bound that precedes it. There is nothing to restamp: every
// byte is already reachable by the mutation engine.
func FuzzSignatureUnmarshalBinary(f *testing.F) {
	fuzzAddCorpus(f, "signature")
	fuzzUniversalSeeds(f)

	f.Fuzz(func(t *testing.T, data []byte) {
		var s Signature
		if err := s.UnmarshalBinary(data); err != nil {
			fuzzRequireSentinel(t, err)
			// Still usable: an untouched Signature is the zero one, which every comparison
			// declines rather than mis-answering.
			require.Equal(t, 0.0, s.Jaccard(s),
				"a rejected buffer must leave the zero signature, which compares 0")
			require.False(t, s.IsNearDup(s, 1.0))
			return
		}

		compact, err := s.MarshalBinary()
		require.NoError(t, err, "a buffer Signature accepted must be re-encodable")
		require.True(t, bytes.Equal(data, compact),
			"the compact form is fully determined by Perms and Mins, so a decoded signature must "+
				"re-encode to the exact bytes it came from")

		var round Signature
		require.NoError(t, round.UnmarshalBinary(compact))
		require.Equal(t, s, round, "marshal ∘ unmarshal must reproduce an equal Signature")

		// Perms == 0 decodes to the ZERO signature — "MinHash was disabled" — which every
		// comparison is required to decline, so the self-match claim applies only above it.
		if s.Perms != 0 {
			require.Equal(t, 1.0, s.Jaccard(s), "a decoded signature must match itself exactly")
			require.True(t, s.IsNearDup(s, 1.0))
			return
		}
		require.Equal(t, 0.0, s.Jaccard(s), "the zero signature must compare 0 against anything")
	})
}

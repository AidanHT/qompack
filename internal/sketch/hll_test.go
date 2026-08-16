package sketch

import (
	"bytes"
	"math"
	"math/bits"
	"strconv"
	"testing"

	"github.com/qompack/qompack/internal/core"
	"github.com/stretchr/testify/require"
)

// These tests are package sketch, not package sketch_test, because TestHLL_MergeIsExactUnion
// asserts on h.regs directly: "a merged sketch is bit-identical to one built from the union" is a
// statement about the register array, and comparing only the two Cardinality values would pass for
// any merge that happened to land on the same estimate.

const (
	// hllRegisters is Appendix C's sketches.hll.registers.
	hllRegisters = 2_048
	// hllP is log2(hllRegisters): the number of low hash bits that select a register.
	hllP = 11
	// hllFrameBytes is the marshalled frame length: the 32-byte prefix, one param
	// ("registers": 1 + 9 + 8 = 18 bytes), the 2 048-byte register body, and the 4-byte CRC32C.
	hllFrameBytes = 2_102
	// hllMaxRank is the largest rank a register can legitimately hold at p = 11. The suffix Add
	// measures is 64−p bits wide and the rank is its leading-zero count plus one, so an all-zero
	// suffix gives 65−p = 54. It is the ceiling decodeV1 refuses a forged body against.
	hllMaxRank = 65 - hllP
	// hllStdErrNumerator is HyperLogLog's standard-error constant: the relative error is 1.04/√m.
	hllStdErrNumerator = 1.04
	// hllStdErr is 1.04/√2048 = 0.02298…, rounded to three places — §5.7's "~2.3% error".
	hllStdErr = 0.023
	// hllTolerance is the accuracy band every cardinality assertion below uses: 3× the standard
	// error, matching the §15 behaviour table's "within 3×2.3%".
	hllTolerance = 0.07
)

// TestHLL_AlphaTableMatchesFlajolet pins all four bias-correction constants against the published
// values (Flajolet, Fusy, Gandouet & Meunier, "HyperLogLog: the analysis of a near-optimal
// cardinality estimation algorithm", 2007, Fig. 3): α₁₆ = 0.673, α₃₂ = 0.697, α₆₄ = 0.709, and
// α_m = 0.7213/(1 + 1.079/m) for m ≥ 128.
//
// Two of the four are UNREACHABLE through the exported surface — NewHLL clamps to
// MinHLLRegisters = 64 and decodeV1 refuses anything below it, so alpha(16) and alpha(32) are dead
// as far as any sketch this package will ever build is concerned. The table's own comment calls
// itself "a specification, not a tuning knob", and this test is what makes that true of the dead
// two as well as the live two: a reference implementation's constants are checked against the
// reference, not against whichever branches happen to be reachable this release. It is also the
// cheaper of the two available answers, the other being to delete the two arms and lose fidelity to
// the algorithm the file claims to implement.
//
// The closed form is checked at three widths rather than one so that a transcription error in
// either of its two constants is caught: a wrong numerator would move every row by the same factor
// and a wrong bias term would move them by different ones.
func TestHLL_AlphaTableMatchesFlajolet(t *testing.T) {
	// The three measured constants are compared exactly. The closed-form rows carry one ULP of
	// slack and it is not a weakening: Go evaluates an untyped-constant expression at compile time
	// in arbitrary precision and rounds once, while alpha does the same arithmetic in float64 and
	// rounds twice, so the two can differ in the last bit. A transcription error in either constant
	// moves the result by parts in a thousand, four orders of magnitude above this.
	const closedFormULP = 1e-15

	for _, tc := range []struct {
		name  string
		m     int
		want  float64
		delta float64
	}{
		{"m=16 is measured, not derived", 16, 0.673, 0},
		{"m=32 is measured, not derived", 32, 0.697, 0},
		{"m=64 is measured, not derived", 64, 0.709, 0},
		{"m=128 uses the closed form", 128, 0.7213 / (1 + 1.079/128), closedFormULP},
		{"m=2048 is Appendix C's register count", hllRegisters, 0.7213 / (1 + 1.079/2048), closedFormULP},
		{"m=65536 is MaxHLLRegisters", MaxHLLRegisters, 0.7213 / (1 + 1.079/65536), closedFormULP},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.InDelta(t, tc.want, alpha(tc.m), tc.delta,
				"α for m = %d is a published constant", tc.m)
		})
	}

	// The measured small-m values exist BECAUSE the closed form does not reproduce them; if it did,
	// the three case arms would be redundant rather than specified. Stating that here is what stops
	// a future reader "simplifying" alpha down to its default branch.
	for _, m := range []int{16, 32, 64} {
		require.NotEqual(t, 0.7213/(1+1.079/float64(m)), alpha(m),
			"α for m = %d must differ from the closed form, or the special case is pointless", m)
	}

	// And the two the constructor can actually produce are the two the estimator uses.
	require.Equal(t, MinHLLRegisters, NewHLL(16).Registers(),
		"NewHLL clamps below MinHLLRegisters, which is what makes alpha(16) unreachable in practice")
	require.InDelta(t, 0.709, alpha(NewHLL(16).Registers()), 0)
}

// TestHLL_AppendixSizing pins 00-ARCHITECTURE.md §5.7's "2048 registers, ~2KB, ~2.3% error" to the
// exact numbers this implementation produces, at all three of the places that claim is made:
// the register count after clamping and power-of-two rounding, the marshalled frame length, and
// the standard error the register count implies.
//
// The frame arithmetic is asserted from its parts rather than only against the total, because
// 2 102 is a sum of four independent decisions — the fixed prefix, the single param's encoded
// width, the body, and the CRC — and a test that checked only the sum could not say which moved.
func TestHLL_AppendixSizing(t *testing.T) {
	h := NewHLL(hllRegisters)
	require.Equal(t, hllRegisters, h.Registers())
	require.Equal(t, uint8(hllP), h.p, "2048 registers is p = log2(2048) = 11")

	data, err := h.MarshalBinary()
	require.NoError(t, err)
	require.Len(t, data, hllFrameBytes, "§5.7's ~2KB is %d bytes of frame", hllFrameBytes)
	require.Equal(t,
		headerPrefixLen+(1+len(paramRegisters)+paramValueLen)+hllRegisters+crcLen,
		hllFrameBytes,
		"32-byte prefix + one %q param (1+%d+8) + %d-byte body + 4-byte CRC",
		paramRegisters, len(paramRegisters), hllRegisters)

	_, body, err := DecodeHeader(data)
	require.NoError(t, err)
	require.Len(t, body, hllRegisters, "the body is the register bytes verbatim")

	stdErr := hllStdErrNumerator / math.Sqrt(hllRegisters)
	require.Equal(t, hllStdErr, math.Round(stdErr*1_000)/1_000,
		"§5.7's ~2.3%% is 1.04/√%d = %.5f", hllRegisters, stdErr)

	t.Logf("Appendix C (registers=%d): p=%d body=%d bytes frame=%d bytes stdErr=1.04/√%d=%.5f",
		hllRegisters, h.p, len(body), len(data), hllRegisters, stdErr)
}

// TestHLL_RoundsUpToPowerOfTwo pins the register-count normalization. The index is taken as
// x & (m−1), which only selects uniformly when m is a power of two, so a caller's 1 000 must
// become 1 024 rather than being used as-is — a non-power-of-two mask would fold whole ranges of
// hashes onto the same registers and quietly destroy the estimator.
//
// Rounding UP rather than down matters too: down would silently give a caller less accuracy than
// it asked for, and accuracy is the only thing the register count buys.
func TestHLL_RoundsUpToPowerOfTwo(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   int
		want int
	}{
		{"1000 rounds up to 1024", 1_000, 1_024},
		{"3000 rounds up to 4096", 3_000, 4_096},
		{"1 clamps up to MinHLLRegisters", 1, MinHLLRegisters},
		{"1<<20 clamps down to MaxHLLRegisters", 1 << 20, MaxHLLRegisters},
		{"0 clamps up to MinHLLRegisters", 0, MinHLLRegisters},
		{"negative clamps up to MinHLLRegisters", -5, MinHLLRegisters},
		{"an exact power of two is untouched", hllRegisters, hllRegisters},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := NewHLL(tc.in)
			require.Equal(t, tc.want, h.Registers())
			require.Len(t, h.regs, tc.want)
			require.Zero(t, tc.want&(tc.want-1), "the register count must be a power of two")
			require.Equal(t, uint8(bits.TrailingZeros(uint(tc.want))), h.p, "p must be log2(m)")

			// A clamped constructor still produces a usable sketch: §12.3, no constructor in this
			// package panics or returns an error.
			h.Add([]byte("probe"))
			require.Positive(t, h.Cardinality())
		})
	}
}

// TestHLL_EmptyCardinality pins the empty sketch to 0. This is not free: with every register at 0
// the raw estimator computes α·m²/m = α·m ≈ 1 476, so an implementation that skipped the
// small-range branch would report a fresh sketch as having seen fifteen hundred distinct keys —
// and SP-08's exploration-breadth signal would start every session already saturated.
func TestHLL_EmptyCardinality(t *testing.T) {
	require.Equal(t, uint64(0), NewHLL(hllRegisters).Cardinality())
	require.Equal(t, uint64(0), NewHLL(MinHLLRegisters).Cardinality())
}

// TestHLL_SmallRangeLinearCounting exercises the branch the test above only proved at zero. At 100
// distinct keys in 2 048 registers most registers are still empty, the raw estimator is badly
// biased, and linear counting — m·ln(m/zeros) — is what makes the answer usable. 5 % is well
// inside the 3σ band the larger cardinalities are held to, because linear counting is far more
// accurate than the raw estimator in exactly this regime.
//
// Observed on the frozen domain-separated SHA-256 hashing, recorded from the first green run:
// Cardinality() = 96, relative error 0.0400 against the 0.05 bound. That margin is thin, and
// deliberately noted: the figure is deterministic, so any change to the "small-" key prefix or to
// domainHLL will move it and may trip this bound. If that happens the cause is the fixture, not the
// estimator — re-record the observation here rather than widening the bound.
func TestHLL_SmallRangeLinearCounting(t *testing.T) {
	const n = 100

	h := NewHLL(hllRegisters)
	buf := make([]byte, 0, 32)
	for i := 0; i < n; i++ {
		buf = hllKey(buf, "small-", i)
		h.Add(buf)
	}

	got := h.Cardinality()
	rel := math.Abs(float64(got)-n) / n
	t.Logf("linear counting at n=%d: Cardinality()=%d, relative error %.4f", n, got, rel)
	require.LessOrEqual(t, rel, 0.05, "the small-range branch must be within 5%% at n=%d", n)
}

// TestHLL_ErrorBounds is the accuracy test proper: four cardinalities spanning three orders of
// magnitude, each held to 3× the 2.3 % standard error. The top row runs to a million keys because
// that is the regime the sketch actually exists for — §5.7 sizes explore.hll at 2 KB precisely so
// that a session exploring a million distinct paths still costs two kilobytes.
//
// Observed relative errors on the frozen domain-separated SHA-256 hashing, recorded from the first
// green run: n=1 000 → 0.0310 (est 1 031), n=10 000 → 0.0136 (est 9 864), n=100 000 → 0.0080
// (est 99 199), n=1 000 000 → 0.0137 (est 986 294). The worst of the four is 1.35 standard errors,
// so the 3σ bound asserted below has real headroom rather than being fitted to the observations.
func TestHLL_ErrorBounds(t *testing.T) {
	for _, n := range []int{1_000, 10_000, 100_000, 1_000_000} {
		t.Run(strconv.Itoa(n), func(t *testing.T) {
			h := NewHLL(hllRegisters)
			buf := make([]byte, 0, 32)
			for i := 0; i < n; i++ {
				buf = hllKey(buf, "hll-", i)
				h.Add(buf)
			}

			got := h.Cardinality()
			rel := math.Abs(float64(got)-float64(n)) / float64(n)
			t.Logf("n=%d: Cardinality()=%d, relative error %.4f (bound %.2f = 3×%.3f)",
				n, got, rel, hllTolerance, hllStdErr)
			require.LessOrEqual(t, rel, hllTolerance,
				"relative error at n=%d must stay inside 3× the standard error", n)
			require.Equal(t, uint64(n), h.Header().Count, "Count is Add calls, which is n here")
		})
	}
}

// TestHLL_DuplicatesDoNotInflate is the property that makes a cardinality sketch a cardinality
// sketch: re-adding a key sets no register higher than it already is, so ten passes over 500 keys
// answer the same as one. Count, by contrast, is Add calls and does move — the two numbers mean
// different things and a consumer that confused them would read a repeated tool call as new
// exploration.
func TestHLL_DuplicatesDoNotInflate(t *testing.T) {
	const (
		keys   = 500
		passes = 10
	)

	h := NewHLL(hllRegisters)
	buf := make([]byte, 0, 32)
	for p := 0; p < passes; p++ {
		for i := 0; i < keys; i++ {
			buf = hllKey(buf, "dup-", i)
			h.Add(buf)
		}
	}

	got := h.Cardinality()
	rel := math.Abs(float64(got)-keys) / keys
	t.Logf("%d keys added %d times: Cardinality()=%d, relative error %.4f", keys, passes, got, rel)
	require.LessOrEqual(t, rel, hllTolerance)
	require.Equal(t, uint64(keys*passes), h.Header().Count,
		"Count is Add calls, not distinct keys")
}

// TestHLL_MergeIsExactUnion asserts the merge is the exact register-wise max, not an approximation
// of it. Because each register holds the maximum rank seen for the keys that landed on it, the max
// of two register arrays is exactly the array the union of the two streams would have produced —
// so the assertion is byte identity of the arrays, not merely equal cardinality. Two sketches can
// agree on an estimate while disagreeing register by register, and a later merge against a third
// sketch would then diverge; identity is what makes merging associative across sessions (O4).
func TestHLL_MergeIsExactUnion(t *testing.T) {
	const n = 20_000

	h1, h2, h3 := NewHLL(hllRegisters), NewHLL(hllRegisters), NewHLL(hllRegisters)
	buf := make([]byte, 0, 32)
	for i := 0; i < n; i++ {
		buf = hllKey(buf, "merge-", i)
		if i%2 == 0 {
			h1.Add(buf)
		} else {
			h2.Add(buf)
		}
		h3.Add(buf)
	}

	require.NoError(t, h1.MergeFrom(h2))
	require.Equal(t, h3.regs, h1.regs,
		"the register-wise max of two disjoint halves must be byte-identical to the union's")
	require.Equal(t, h3.Cardinality(), h1.Cardinality())
	require.Equal(t, h3.Header(), h1.Header())
}

// TestHLL_MergeShapeMismatch asserts a merge across differing register counts is refused rather
// than attempted. The two arrays index the same key to different registers and carry ranks measured
// against different p, so a positional max would produce an array describing no stream at all —
// and every answer out of it would still look like a plausible cardinality.
func TestHLL_MergeShapeMismatch(t *testing.T) {
	h := NewHLL(hllRegisters)
	require.ErrorIs(t, h.MergeFrom(NewHLL(hllRegisters/2)), ErrShapeMismatch)
	require.ErrorIs(t, h.MergeFrom(nil), ErrShapeMismatch, "a nil source is a shape mismatch, not a panic")
}

// TestHLL_MarshalRoundTrip is the §6.2 promise for the register file: a sketch that cannot be
// re-read is worse than no sketch. Re-marshalling must be BYTE-identical, and the decoded sketch
// must answer with the same cardinality — the registers are the whole of the state, so anything
// less would mean a session's exploration breadth changed by being saved.
func TestHLL_MarshalRoundTrip(t *testing.T) {
	const (
		created = core.UnixMilli(1_700_000_000_000)
		n       = 100_000
	)

	h := NewHLL(hllRegisters)
	h.SetCreated(created)
	buf := make([]byte, 0, 32)
	for i := 0; i < n; i++ {
		buf = hllKey(buf, "rt-", i)
		h.Add(buf)
	}

	data, err := h.MarshalBinary()
	require.NoError(t, err)

	got := NewHLL(MinHLLRegisters)
	require.NoError(t, got.UnmarshalBinary(data))

	again, err := got.MarshalBinary()
	require.NoError(t, err)
	require.Equal(t, data, again, "re-marshalling a decoded sketch must be byte-identical")

	require.Equal(t, hllRegisters, got.Registers())
	require.Equal(t, h.regs, got.regs)
	require.Equal(t, h.Cardinality(), got.Cardinality())
	require.Equal(t, created, got.Header().Created)
	require.Equal(t, h.Header(), got.Header())
}

// TestHLL_UnmarshalRejects walks every way a well-framed QPKS blob can still describe a register
// file this build must refuse to build. Each frame is assembled through EncodeHeader, so it has a
// correct CRC and a self-consistent length block: the only thing wrong with it is the HLL-specific
// claim named in the row.
//
// Two rows matter most. m is the mask Add uses, so a frame claiming 1 000 registers would not
// merely be inaccurate — it would index unevenly for the rest of the sketch's life, and nothing
// downstream could tell. And a register VALUE above the rank ceiling 65−p is the one forged field
// no length check can catch: m and the ranks together are the entire input to Cardinality, and a
// body of 0xFF bytes drives the estimator past 2^277, where the float→uint64 conversion is
// implementation-defined in Go (0x8000000000000000 on amd64, saturating on arm64).
func TestHLL_UnmarshalRejects(t *testing.T) {
	for _, tc := range []struct {
		name      string
		registers float64
		body      []byte
		wantErr   error
	}{
		{"registers not a power of two", 1_000, make([]byte, 1_000), ErrMalformed},
		{"registers above MaxHLLRegisters", 1 << 20, make([]byte, hllRegisters), ErrMalformed},
		{"registers below MinHLLRegisters", MinHLLRegisters / 2, make([]byte, MinHLLRegisters/2), ErrMalformed},
		{"registers zero", 0, nil, ErrMalformed},
		{"registers not an integer", 2_048.5, make([]byte, hllRegisters), ErrMalformed},
		{"body shorter than registers", hllRegisters, make([]byte, hllRegisters-1), ErrTruncated},
		{"body longer than registers", hllRegisters, make([]byte, hllRegisters+1), ErrTruncated},
		{"every register forged to 0xFF", hllRegisters, bytes.Repeat([]byte{0xFF}, hllRegisters), ErrMalformed},
		{"one register one rank above the ceiling", hllRegisters, hllForgedBody(hllMaxRank + 1), ErrMalformed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			frame, err := EncodeHeader(Header{
				Ver:    FormatVersion,
				Kind:   KindHLL,
				Params: map[string]float64{paramRegisters: tc.registers},
			}, tc.body)
			require.NoError(t, err, "fixture sanity: the frame itself must be well-formed")

			h := NewHLL(hllRegisters)
			require.ErrorIs(t, h.UnmarshalBinary(frame), tc.wantErr)
			require.Equal(t, hllRegisters, h.Registers(),
				"a refused frame must not half-replace the sketch it was loaded into")
		})
	}

	// The rank ceiling is EXACT, not approximate: 65−p is what an all-zero hash suffix produces, so
	// a register holding exactly that must still decode. A check that rejected it would refuse a
	// legitimate sketch — rarely, and only after the sketch had been in service long enough to see
	// such a suffix, which is the kind of bug that only ever appears in production.
	t.Run("a register at exactly the ceiling decodes", func(t *testing.T) {
		frame, err := EncodeHeader(Header{
			Ver:    FormatVersion,
			Kind:   KindHLL,
			Params: map[string]float64{paramRegisters: hllRegisters},
		}, hllForgedBody(hllMaxRank))
		require.NoError(t, err)

		h := NewHLL(hllRegisters)
		require.NoError(t, h.UnmarshalBinary(frame))
		require.Equal(t, uint8(hllMaxRank), h.regs[hllRegisters-1],
			"a rank of exactly 65−p is legitimate and must survive the round trip")
	})

	// The second line of defence. decodeV1 refuses a forged body outright, but Cardinality must give
	// a DEFINED answer for a register array that reached memory by any other route, because the
	// conversion at the end of it is implementation-defined for an out-of-range float64 and
	// "whatever amd64 happens to do" is not an acceptable answer for a cross-platform binary. The
	// clamp is maxTotalFloat = 2^62, the same ceiling Scale uses, chosen because it round-trips
	// float64 exactly where math.MaxUint64 does not.
	t.Run("a forged register array still yields a defined cardinality", func(t *testing.T) {
		h := NewHLL(hllRegisters)
		for i := range h.regs {
			h.regs[i] = 0xFF
		}

		got := h.Cardinality()
		t.Logf("all-0xFF registers: Cardinality() = %d (2^62, the maxTotalFloat clamp)", got)
		require.Equal(t, uint64(1)<<62, got,
			"an out-of-range estimate must clamp rather than convert; the answer must be identical on every platform")
		require.Equal(t, got, h.Cardinality(), "and it must be stable across calls")
	})

	// A CMS frame loaded into an *HLL is refused by kind, not misread as a register file whose
	// body happens to be the right length.
	frame, err := EncodeHeader(Header{
		Ver:    FormatVersion,
		Kind:   KindCMS,
		Params: map[string]float64{paramDelta: cmsDelta, paramDepth: cmsDepth, paramEpsilon: cmsEpsilon, paramWidth: cmsWidth},
	}, make([]byte, cmsBodyBytes))
	require.NoError(t, err)
	require.ErrorIs(t, NewHLL(hllRegisters).UnmarshalBinary(frame), ErrKindMismatch)

	// A nil receiver reports rather than panics: §12.3, a hook that dies takes observability with
	// it. The same rule covers MarshalBinary, whose only other option would be a nil dereference.
	var nilHLL *HLL
	require.ErrorIs(t, nilHLL.UnmarshalBinary(nil), ErrMalformed)
	_, err = nilHLL.MarshalBinary()
	require.ErrorIs(t, err, ErrMalformed)

	// An UNSIZED sketch is refused for the reason bloom.go and misragries.go state: marshallable
	// means re-readable. Without this guard `Save(p, &HLL{})` wrote a perfectly valid 54-byte frame
	// declaring registers = 0, which this very decoder then refuses forever as
	// "param registers = 0 outside [64, 65536]" — a sketch that cannot survive a restart, which
	// doc.go says cannot happen. The assertion is here as well as in RunSketchSuite because the
	// suite states the contract generically and this states which number makes THIS type unsized.
	t.Run("an unsized sketch is not written", func(t *testing.T) {
		unsized, unsizedErr := new(HLL).MarshalBinary()
		require.ErrorIs(t, unsizedErr, ErrMalformed)
		require.Nil(t, unsized, "nothing is written when nothing can be read back")

		// The decoder's half of the same statement: had it been written, this is what would have
		// happened on the next restart.
		sized, sizedErr := NewHLL(hllRegisters).MarshalBinary()
		require.NoError(t, sizedErr)
		require.NoError(t, NewHLL(hllRegisters).UnmarshalBinary(sized),
			"fixture sanity: a SIZED sketch's frame does decode, so the refusal above is about the "+
				"register count and not about the encoding")
	})
}

// hllForgedBody returns a register body that is entirely legal except for its LAST byte, which
// holds rank. Putting the one bad byte at the end rather than the start is deliberate: it proves
// the decoder scans the whole body rather than sampling the front of it, and it isolates the value
// check from the length check, since everything else about the frame is correct.
func hllForgedBody(rank byte) []byte {
	body := make([]byte, hllRegisters)
	body[len(body)-1] = rank
	return body
}

// hllKey appends i to prefix, reusing buf's array. TestHLL_ErrorBounds runs to n = 1 000 000 keys
// and the file builds roughly 1.2 million keys in total, where a fmt.Sprintf per key would cost
// more than all the hashing put together; strconv.AppendInt into a reused buffer keeps the file's
// runtime dominated by the SHA-256 calls that are actually under test.
func hllKey(buf []byte, prefix string, i int) []byte {
	buf = append(buf[:0], prefix...)
	return strconv.AppendInt(buf, int64(i), 10)
}

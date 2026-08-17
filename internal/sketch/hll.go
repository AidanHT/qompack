package sketch

import (
	"fmt"
	"math"
	"math/bits"

	"github.com/qompack/qompack/internal/core"
)

// paramRegisters is the on-wire name of the register count, the single param an HLL frame carries.
// It is lower case because the QPKS frame restricts param names to [a-z0-9.] (header.go), and it is
// a constant rather than a literal at three call sites because the encoder and the decoder have to
// agree: a typo in one of them would produce a sketch that saves cleanly and then fails to load,
// which is the one failure §6.2 exists to prevent.
const paramRegisters = "registers"

// The bias-correction constants of the HyperLogLog estimator (Flajolet et al.). The three small
// register counts have their own measured α; every larger m uses the closed form. They are named
// rather than spelled inline because they are a specification, not a tuning knob: changing one
// changes every cardinality this package has ever reported, and a warm-started session would then
// disagree with the file it was warm-started from.
const (
	// alpha16 is α for m = 16.
	alpha16 = 0.673
	// alpha32 is α for m = 32.
	alpha32 = 0.697
	// alpha64 is α for m = 64.
	alpha64 = 0.709
	// alphaNumerator and alphaBiasTerm form the closed form 0.7213/(1 + 1.079/m) used for m ≥ 128.
	alphaNumerator = 0.7213
	alphaBiasTerm  = 1.079
)

// rankHashBits is the width of the hash word Add measures a rank against. It is named because the
// decoder's rank ceiling is derived from it: the suffix is 64−p bits wide and the rank is its
// leading-zero count plus one, so 65−p is the largest value any register can legitimately hold.
const rankHashBits = 64

// linearCountingThreshold is the raw estimate, in multiples of m, below which the harmonic-mean
// estimator is replaced by linear counting. Below 2.5·m most registers are still empty and the raw
// estimator is badly biased; m·ln(m/zeros) is exact-ish in exactly that regime. Without this branch
// a freshly constructed sketch would report α·m ≈ 1 476 distinct keys rather than 0, and SP-08's
// exploration-breadth signal would start every session already saturated.
const linearCountingThreshold = 2.5

// HLL is the HyperLogLog cardinality sketch behind sketches/explore.hll (00-ARCHITECTURE.md §5.7):
// how many DISTINCT paths or symbols a session has explored, in a fixed 2 KiB whatever the answer
// turns out to be.
//
// Its accuracy is a standard error of 1.04/√m — 2.3 % at Appendix C's 2 048 registers — and the
// error is relative, so the sketch costs the same two kilobytes at a thousand distinct paths as at
// a million. Per §13 invariant 3 the answer is a cache and never the source of truth.
//
// MergeFrom is the exact register-wise max, which is what lets Phase 7's cross-session warm start
// (O4) combine a project's historical exploration with the current session's without either
// double-counting the overlap or losing it.
//
// HLL is NOT safe for concurrent use. Add mutates the register array in place and Count is not
// atomic; the daemon (SP-05) owns every live sketch behind its session-registry mutex.
type HLL struct {
	p       uint8   // log2(m)
	regs    []uint8 // len == m, each holding the maximum rank seen for that register
	count   uint64  // Add calls, NOT cardinality
	created core.UnixMilli
}

// NewHLL returns an HLL with registers registers, clamped to [MinHLLRegisters, MaxHLLRegisters] and
// then rounded UP to a power of two. Appendix C's 2 048 gives p = 11, a 2 048-byte body, a
// 2 102-byte frame and a standard error of 1.04/√2048 = 2.3 %, matching §5.7's "2048 registers,
// ~2KB, ~2.3% error".
//
// The power-of-two rounding is not cosmetic. Add selects a register with x & (m−1), which only
// distributes uniformly when m is a power of two; a caller's 1 000 used as-is would fold whole
// ranges of hashes onto the same registers and quietly destroy the estimator. Rounding UP rather
// than down is deliberate too — down would silently hand back less accuracy than was asked for, and
// accuracy is the only thing the register count buys.
//
// It never panics and never returns an error: out-of-range arguments are clamped, because a hook
// that dies takes observability down with it (§12.3). Registers reports what the caller got.
func NewHLL(registers int) *HLL {
	m := clampInt(registers, MinHLLRegisters, MaxHLLRegisters)
	if m&(m-1) != 0 {
		m = 1 << bits.Len(uint(m-1))
	}
	// MaxHLLRegisters is itself a power of two, so the round-up above cannot cross it; the clamp is
	// repeated anyway because the ceiling rule (errors.go) must hold for every path into make().
	if m > MaxHLLRegisters {
		m = MaxHLLRegisters
	}
	return &HLL{p: uint8(bits.TrailingZeros(uint(m))), regs: make([]uint8, m)}
}

// Add records key. It is idempotent in the quantity that matters: adding a key already seen sets no
// register higher than it already is, so Cardinality does not move. Count does — it is the number
// of Add calls, and the two mean different things.
//
// The low p bits of the hash select the register and the remaining 64−p bits supply the rank: the
// position of the first 1-bit, which is geometrically distributed and is therefore a sample of
// log2 of the number of distinct keys that landed there. Only ONE hash is computed per key, which
// is what keeps SP-08's observer hook inside its §8.1 budget.
func (h *HLL) Add(key []byte) {
	if len(h.regs) == 0 {
		// The zero HLL has no register array, and the mask below would underflow to all-ones.
		// Silently doing nothing is the only non-panicking answer, and it agrees with Cardinality,
		// which reports 0.
		return
	}
	x, _ := hash128(domainHLL, key)
	idx := x & (uint64(len(h.regs)) - 1)
	w := x >> h.p
	// w carries p leading zeros from the shift itself, so they are subtracted back out. w == 0 —
	// the whole remaining suffix was zero — gives the maximum rank, 65−p.
	rho := uint8(bits.LeadingZeros64(w) - int(h.p) + 1)
	if rho > h.regs[idx] {
		h.regs[idx] = rho
	}
	h.count++
}

// Cardinality estimates the number of distinct keys added, as the bias-corrected harmonic mean of
// the register ranks, falling back to linear counting while most registers are still empty.
//
// There is deliberately NO large-range correction. The original HyperLogLog needs one because a
// 32-bit hash saturates near 2^32 distinct values; hash128 supplies 64 bits, so that regime is
// unreachable at any cardinality this system can produce — a session would have to explore four
// billion distinct paths before it began to matter.
func (h *HLL) Cardinality() uint64 {
	if len(h.regs) == 0 {
		return 0
	}
	m := float64(len(h.regs))
	var sum float64
	var zeros int
	for _, r := range h.regs {
		sum += math.Ldexp(1, -int(r)) // 2^-r
		if r == 0 {
			zeros++
		}
	}

	est := alpha(len(h.regs)) * m * m / sum
	if zeros > 0 && est <= linearCountingThreshold*m {
		est = m * math.Log(m/float64(zeros))
	}
	// The clamp before the conversion is an UPWARD one, correcting the plan's `if est < 0`. That
	// guard is dead: alpha, m and sum are all strictly positive, so the raw estimator is positive,
	// and m·ln(m/zeros) is non-negative because zeros ≤ m. The reachable hazard is the opposite one.
	// A register array holding ranks no real sketch can produce — 0xFF bytes from a forged frame —
	// makes sum ≈ m·2^-255 and drives est past 2^277, and converting an out-of-range float64 to
	// uint64 is implementation-defined in Go: 0x8000000000000000 on amd64, saturating on arm64. This
	// package ships cross-platform, so the answer has to be the same on both. maxTotalFloat is the
	// same bound Scale uses and for the same reason: 2^62 round-trips float64 exactly, where
	// math.MaxUint64 does not. decodeV1 refuses such a frame outright; this is the second line, for
	// a register array that reached memory by any other route.
	if est > maxTotalFloat {
		est = maxTotalFloat
	}
	return uint64(math.Round(est))
}

// alpha returns the bias-correction constant for m registers. The three small cases are measured
// values that the closed form does not reproduce; everything from 128 up uses the closed form.
func alpha(m int) float64 {
	switch m {
	case 16:
		return alpha16
	case 32:
		return alpha32
	case 64:
		return alpha64
	default:
		return alphaNumerator / (1 + alphaBiasTerm/float64(m))
	}
}

// Registers returns the register count AFTER clamping and power-of-two rounding — the m of the
// 1.04/√m standard error. Reporting the realised count rather than the requested one is what makes
// a silently corrected configuration visible.
func (h *HLL) Registers() int { return len(h.regs) }

// SetCreated stamps the construction time carried in the on-disk header. It is a setter rather than
// a NewHLL argument because this package takes no core.Clock: the composition root that owns the
// clock stamps the sketch, and every test that needs a byte-stable frame can pin the value without
// a fake clock.
func (h *HLL) SetCreated(t core.UnixMilli) { h.created = t }

// MergeFrom takes the register-wise maximum of o into h. It is EXACT: each register already holds
// the maximum rank seen for the keys that landed on it, so the max of two register arrays is
// precisely the array the union of the two streams would have produced. A merged sketch is
// bit-identical to one built from the union, which is what makes merging associative — and
// associativity is what lets Phase 7's O4 warm start fold any number of past sessions together in
// any order and get the same answer.
//
// The register counts must match. Two arrays built with different p index the same key to different
// registers and carry ranks measured against different suffix lengths, so a positional max would
// produce an array describing no stream at all. A nil source is the same refusal, not a panic.
func (h *HLL) MergeFrom(o *HLL) error {
	if o == nil {
		return fmt.Errorf("%w: MergeFrom(nil)", ErrShapeMismatch)
	}
	if o.p != h.p || len(o.regs) != len(h.regs) {
		return fmt.Errorf("%w: cannot merge %d registers into %d",
			ErrShapeMismatch, len(o.regs), len(h.regs))
	}
	for i, r := range o.regs {
		if r > h.regs[i] {
			h.regs[i] = r
		}
	}
	h.count = satAdd64(h.count, o.count)
	return nil
}

// Header returns the on-disk metadata for this sketch: Kind KindHLL, the Add count as Count, the
// Created stamp, and the single param that makes the frame self-describing without consulting
// config. CRC32C is zero, as Sketch documents — the checksum covers the encoded body and is known
// only to DecodeHeader.
func (h *HLL) Header() Header {
	return Header{
		Magic:   HeaderMagic,
		Ver:     FormatVersion,
		Kind:    KindHLL,
		Params:  map[string]float64{paramRegisters: float64(len(h.regs))},
		Count:   h.count,
		Created: h.created,
	}
}

// MarshalBinary encodes the sketch as a QPKS frame: the header above, then the register bytes
// verbatim. One byte per register is the whole body, so the encoding has no endianness question and
// a given logical state has exactly one representation — which is what makes the frozen golden
// fixtures and the byte-identical re-marshal assertion mean anything.
//
// A nil receiver reports ErrMalformed rather than dereferencing. §12.3: a hook that dies takes
// observability down with it, so every entry point in this package fails by reporting.
//
// An UNSIZED sketch — the zero HLL, reachable as `var h sketch.HLL` from outside this package — is
// refused for the reason bloom.go states: the ceiling rule in errors.go says every constructible
// sketch must also be MARSHALLABLE, and marshallable means re-readable, not merely writable. A zero
// HLL would otherwise emit a structurally valid, correctly checksummed 54-byte frame declaring
// registers = 0, which decodeV1 then refuses forever as "param registers = 0 outside [64, 65536]".
// Writing a file this build's own decoder is guaranteed to reject is worse than refusing to write
// it: the refusal is loud and recoverable at the call site, whereas the file is discovered dead on
// the next restart — the one failure §6.2 exists to prevent, and the one doc.go promises cannot
// happen. RunSketchSuite holds every sketch type to this, so SP-16's factories inherit it.
func (h *HLL) MarshalBinary() ([]byte, error) {
	if h == nil {
		return nil, fmt.Errorf("%w: MarshalBinary on a nil *HLL", ErrMalformed)
	}
	if len(h.regs) == 0 {
		return nil, fmt.Errorf(
			"%w: MarshalBinary on an unsized HLL (registers=0); construct it with NewHLL", ErrMalformed)
	}
	return EncodeHeader(h.Header(), h.regs)
}

// UnmarshalBinary replaces h's entire state with the sketch encoded in data, or leaves h untouched
// and reports why it could not.
//
// The version switch is the upgrade path header.go documents: case 1 decodes the layout this build
// writes, and when FormatVersion ever becomes 2 a case 2 arm is added while case 1 stays forever,
// because sketches are permanent memory (§6.2) and the oldest file on disk must still decode.
// DecodeHeader has already refused anything above FormatVersion, so the default arm is reachable
// only if that guarantee is ever broken — which is exactly when a bare sentinel beats a panic.
//
// A nil receiver reports ErrMalformed rather than panicking, for the same reason MarshalBinary does.
func (h *HLL) UnmarshalBinary(data []byte) error {
	if h == nil {
		return fmt.Errorf("%w: UnmarshalBinary on a nil *HLL", ErrMalformed)
	}
	hdr, body, err := DecodeHeader(data)
	if err != nil {
		return err
	}
	if hdr.Kind != KindHLL {
		return fmt.Errorf("%w: frame declares %s, want %s", ErrKindMismatch, hdr.Kind, KindHLL)
	}
	switch hdr.Ver {
	case 1:
		return h.decodeV1(hdr, body)
	default:
		return fmt.Errorf("%w: version %d", ErrUnsupportedVersion, hdr.Ver)
	}
}

// decodeV1 decodes the FormatVersion 1 layout.
//
// The register count is validated against the constructor's own bounds AND against the
// power-of-two requirement BEFORE a single byte is allocated, because a forged header is the whole
// of this function's threat model: m is both the make() length and the mask Add will use for the
// rest of the sketch's life. A frame claiming 1 000 registers is not merely inaccurate — it would
// index unevenly forever, and nothing downstream could tell. The body-length check comes after
// those, so a frame that is wrong in two ways reports the structural problem (ErrMalformed) rather
// than the downstream length disagreement it causes (ErrTruncated).
//
// The register values are then checked against the rank ceiling 65−p, because m and the ranks are
// together the entire input to Cardinality's estimator and a forged rank is the one forged field a
// length check cannot catch. That scan runs last of the four, since it needs both p and a body
// whose length is already known to agree — and it still precedes the only allocation here, so every
// rejection path in this function costs nothing.
func (h *HLL) decodeV1(hdr Header, body []byte) error {
	m, err := hdr.MustParamInt(paramRegisters, MinHLLRegisters, MaxHLLRegisters)
	if err != nil {
		return err
	}
	if m&(m-1) != 0 {
		return fmt.Errorf("%w: param %q = %d is not a power of two", ErrMalformed, paramRegisters, m)
	}
	if len(body) != m {
		return fmt.Errorf("%w: body of %d bytes cannot hold %d registers", ErrTruncated, len(body), m)
	}

	// The register VALUES are validated too, not just their count. A rank is the leading-zero count
	// of a 64−p bit suffix plus one, so 65−p is the largest value Add can ever write; anything above
	// it is unreachable for any sketch this package built, which makes this a true structural check
	// rather than a heuristic. It matters because m and the ranks together are the whole input to
	// Cardinality's estimator: a body of 0xFF bytes would otherwise drive the estimate past 2^277
	// and into a float→uint64 conversion Go leaves implementation-defined. The scan is over the body
	// this function has already length-checked and allocates nothing, so a forged frame is still
	// refused before the make() below.
	p := uint8(bits.TrailingZeros(uint(m)))
	maxRank := uint8(rankHashBits - int(p) + 1)
	for i, r := range body {
		if r > maxRank {
			return fmt.Errorf("%w: register %d holds rank %d, above the maximum %d for p = %d",
				ErrMalformed, i, r, maxRank, p)
		}
	}

	// The body is a subslice of the caller's buffer (DecodeHeader), so this copies rather than
	// retaining it.
	regs := make([]uint8, m)
	copy(regs, body)

	h.p = p
	h.regs = regs
	h.count = hdr.Count
	h.created = hdr.Created
	return nil
}

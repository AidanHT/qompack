package sketch

import (
	"encoding/binary"
	"fmt"
	"iter"
	"math"
	"math/bits"

	"github.com/qompack/qompack/internal/core"
)

// The bit array is addressed as uint64 words, so these four numbers are the whole of the
// bit-to-word arithmetic and are named rather than spelled inline: the encoder, the decoder, the
// probe loop and the sizing round-up must provably agree on all four, and a 6 that should have
// been a 3 would produce a filter that still passes every no-false-negative test while silently
// using an eighth of its bits.
const (
	// bitsPerByte converts a bit count to the body length that carries it.
	bitsPerByte = 8
	// wordBits is the width of one word of the bit array.
	wordBits = 64
	// wordBytes is the encoded width of one word in the marshalled body.
	wordBytes = wordBits / bitsPerByte
	// wordShift is log2(wordBits): bit index >> wordShift is its word index.
	wordShift = 6
	// wordMask isolates a bit's offset within its word: index & wordMask.
	wordMask = wordBits - 1
)

// The clamping bounds NewBloom applies. They exist because a constructor in this package never
// panics and never returns an error (util.go): every out-of-range, NaN or infinite argument
// becomes the nearest legal value instead, and these name what "legal" is.
const (
	// minBloomFPRate is the floor on p. Below it the sizing formula produces a filter far larger
	// than any negative-knowledge workload justifies, and 1e-6 is already one false positive in a
	// million eliminations.
	minBloomFPRate = 1e-6
	// maxBloomFPRate is the ceiling on p. Clamping at 0.5 rather than at some value just below 1
	// is deliberate: above p = 0.5 the filter is worse than a coin flip and the sizing formula
	// degenerates, so the nearest USEFUL legal value is the right correction. Capacity() returns
	// the clamped pair, so a caller can always see what it actually got.
	maxBloomFPRate = 0.5
	// maxBloomHashes is the ceiling on k. A k above the word width buys nothing and would let a
	// forged header make Add and Test walk an unbounded probe loop.
	maxBloomHashes = 64
)

// ResizeFillThreshold is the fill ratio at which ResizeTarget starts asking for a bigger filter.
// It is 0.5 rather than something closer to saturation because 00-ARCHITECTURE.md §12's mitigation
// is "monitor fill ratio; resize with a rebuild from eliminated[] in checkpoints" — and a resize
// that only fires once the filter is already saturated is too late, since every elimination
// recorded in the meantime was recorded at a degraded false-positive rate.
const ResizeFillThreshold = 0.5

// ResizeGrowthFactor is the multiple ResizeTarget grows capacity by, capped at MaxBloomCapacity.
// Doubling keeps the number of rebuilds logarithmic in the eventual entry count, and §8.3 makes
// each rebuild cheap: it is a linear pass over a few thousand active elimination records.
const ResizeGrowthFactor = 2

// FPWarnRate is §11.4's watch-for threshold: "At 1% they are safe; at 10% the agent starts
// skipping viable approaches. Monitor fill ratio and resize." Saturated reports whether the
// filter's own estimate has reached it.
const FPWarnRate = 0.10 //nomagic:allow §11.4 bloom false-positive watch threshold; unrelated to scheduler.cache.readMultiplier

// The on-wire param names. They are lower case because the QPKS frame restricts param names to
// [a-z0-9.] (header.go), so the Go field fpRate and the Appendix C key sketches.bloom.fpRate both
// fold to "fprate" on disk. They are constants rather than literals at four call sites because the
// encoder and the decoder have to agree: a typo in one of them would produce a filter that saves
// cleanly and then fails to load, which is the one failure §6.2 exists to prevent.
const (
	// paramCapacity is the configured entry count n.
	paramCapacity = "capacity"
	// paramFPRate is the configured false-positive rate p.
	paramFPRate = "fprate"
	// paramK is the number of hash probes per key.
	paramK = "k"
	// paramM is the bit-array length in bits.
	paramM = "m"
)

// Bloom is the membership sketch behind sketches/tried.bloom (00-ARCHITECTURE.md §5.7,
// Appendix A). It answers "has this been tried?" for §8.3's negative knowledge in a few kilobytes,
// with no false negatives and a bounded, measurable false-positive rate.
//
// Per §13 invariant 3 a Bloom filter is never the source of truth: a true from Test must be backed
// by a record lookup or explicitly flagged BloomOnly. A false, by contrast, is exact — the filter
// never forgets a key it was given.
//
// Bloom is NOT safe for concurrent use. Add mutates the bit array in place and Count is not
// atomic; the daemon (SP-05) owns every live sketch behind its session-registry mutex.
type Bloom struct {
	mBits    uint64 // rounded up to a multiple of wordBits
	k        uint8
	words    []uint64
	count    uint64
	capacity int     // configured n
	fpRate   float64 // configured p
	created  core.UnixMilli
}

// NewBloom returns a Bloom sized by Appendix A's formulas for capacity expected entries at fpRate
// false positives:
//
//	m = −n·ln(p) / (ln 2)²          k = (m/n)·ln 2
//
// For Appendix A's worked example (10 000, 0.01) that is m = 95 851 bits before alignment,
// k = 7, and m = 95 872 bits — 1 498 words, an 11 984-byte body — after rounding up to a whole
// word, matching Appendix A's "m ≈ 95_850 bits ≈ 12 KB, k = 7".
//
// It never panics and never returns an error. Arguments are clamped, not rejected: p goes through
// clamp rather than a pair of comparisons precisely because a NaN must become minBloomFPRate
// rather than flowing into math.Log and then into uint64(NaN), which is undefined in Go and would
// crash the observer hot path (§12.3). Capacity reports the clamped pair back.
func NewBloom(capacity int, fpRate float64) *Bloom {
	n := clampInt(capacity, 1, MaxBloomCapacity)
	p := clamp(fpRate, minBloomFPRate, maxBloomFPRate)
	_, k, mBits := bloomSizing(n, p)

	return &Bloom{
		mBits:    mBits,
		k:        uint8(k),
		words:    make([]uint64, mBits/wordBits),
		capacity: n,
		fpRate:   p,
	}
}

// bloomSizing is Appendix A's arithmetic, and nothing else: it takes an ALREADY-CLAMPED n and p
// and returns the three numbers Appendix A's worked example names. NewBloom keeps mRaw only long
// enough to derive k and mBits from it, so the pre-rounding value would otherwise be unobservable
// — and 95 851 is a number this subplan's Definition of Done states explicitly. Factoring it out
// lets TestBloom_AppendixASizing pin the real value the implementation computed rather than pin
// its own recomputation of the same formula and bracket the implementation to the word-aligned
// 95 872, which is satisfied by any mRaw in (95 808, 95 872].
//
// It is unexported because n and p must already be clamped: passing a NaN p here would reach
// math.Log and then uint64(NaN), which is undefined in Go. NewBloom is the only production caller
// and it clamps first.
func bloomSizing(n int, p float64) (mRaw float64, k int, mBits uint64) {
	// m = −n·ln(p) / (ln 2)²
	mRaw = math.Ceil(-float64(n) * math.Log(p) / (math.Ln2 * math.Ln2))
	// k = (m/n)·ln 2
	k = clampInt(int(math.Round(mRaw/float64(n)*math.Ln2)), 1, maxBloomHashes)

	// Round up to a whole word, then hold the result inside the ceiling rule (errors.go): the
	// largest filter a constructor will build must still marshal to a frame under MaxFrameBytes.
	mBits = (uint64(mRaw) + wordBits - 1) &^ wordMask
	if mBits > MaxBloomBits {
		mBits = MaxBloomBits
	}
	if mBits == 0 {
		mBits = wordBits
	}
	return mRaw, k, mBits
}

// RebuildBloom returns a fresh Bloom sized for capacity and fpRate, holding every key keys yields.
//
// This is §8.3's rebuild path and §12's resize mitigation in one function. When a dependency hash
// changes, the eliminations that depended on it flip to stale and tried.bloom is rebuilt from the
// ACTIVE records only — cheap, because that is a linear pass over a few thousand structured
// entries. Passing a larger capacity is how a saturated filter is resized: there is no in-place
// grow, because a Bloom's bits cannot be redistributed without the original keys.
//
// A nil keys is treated as an empty sequence, so the degenerate rebuild — every elimination went
// stale at once — yields a usable empty filter rather than panicking the idle-window rebuild.
func RebuildBloom(capacity int, fpRate float64, keys iter.Seq[[]byte]) *Bloom {
	b := NewBloom(capacity, fpRate)
	if keys == nil {
		return b
	}
	for k := range keys {
		b.Add(k)
	}
	return b
}

// Add records key. It is idempotent: adding a key already present sets no new bits and does not
// move Count.
//
// The k probe positions come from Kirsch-Mitzenmacher enhanced double hashing,
// g_i(x) = h1 + i·h2 + i², over the single domain-separated SHA-256 hash128 returns. That is what
// makes a k=7 filter cost one hash rather than seven, which is what keeps the observer hook inside
// its §8.1 budget. The i² term is the "enhanced" part: without it the probe positions of two keys
// that happen to share h2 stay in lockstep, and the measured false-positive rate drifts above the
// analytic one.
func (b *Bloom) Add(key []byte) {
	if b.mBits == 0 {
		// The zero Bloom has no bit array. Silently doing nothing is the only non-panicking
		// answer, and it is consistent with Test, which reports false for everything.
		return
	}
	h1, h2 := hash128(domainBloom, key)
	fresh := false
	for i := uint64(0); i < uint64(b.k); i++ {
		idx := (h1 + i*h2 + i*i) % b.mBits
		w, bit := idx>>wordShift, uint64(1)<<(idx&wordMask)
		if b.words[w]&bit == 0 {
			b.words[w] |= bit
			fresh = true
		}
	}
	if fresh {
		b.count = satAdd64(b.count, 1)
	}
}

// Test reports whether key may have been added. false is exact — a Bloom filter has no false
// negatives, which is the property §8.3 relies on to never re-propose an eliminated approach.
// true carries the filter's false-positive probability and, per §13 invariant 3, must be backed by
// a record lookup or explicitly flagged BloomOnly before anything acts on it.
func (b *Bloom) Test(key []byte) bool {
	if b.mBits == 0 {
		return false
	}
	h1, h2 := hash128(domainBloom, key)
	for i := uint64(0); i < uint64(b.k); i++ {
		idx := (h1 + i*h2 + i*i) % b.mBits
		if b.words[idx>>wordShift]&(uint64(1)<<(idx&wordMask)) == 0 {
			return false
		}
	}
	return true
}

// Count returns the number of Add calls that flipped at least one bit.
//
// It is exact for a filter with no insert-time false positives, and undercounts by exactly the
// number of keys whose k bits were all already set — an event with the filter's own
// false-positive probability, i.e. ~1 in 100 at the configured 0.01. It is therefore a lower bound
// on the distinct keys held, never an upper one, which is the safe direction: a Count that
// overstated would make a saturated filter look like it still had room.
//
// The saturating clamp is the other half of satAdd64's job. A count that wrapped into a negative
// int would make a full filter report itself empty, and a sketch that reports itself empty is one
// nothing will ever rebuild.
func (b *Bloom) Count() int {
	if b.count > math.MaxInt {
		return math.MaxInt
	}
	return int(b.count)
}

// Bits returns the length of the bit array, always a multiple of wordBits, together with the
// number of probes per key. m is the m of Appendix A after the word-alignment round-up, and the
// number the marshalled body length is derived from; k is Appendix A's k.
//
// The two are returned together on purpose rather than as separate accessors: every caller that
// wants the bit-array length is sizing or reasoning about something — a rebuild, a saturation
// estimate, a status line — and needs the probe count in the same breath, since neither figure
// means anything about a filter's accuracy without the other.
func (b *Bloom) Bits() (m uint64, k uint8) { return b.mBits, b.k }

// Capacity returns the entry count and false-positive rate b was constructed with, AFTER clamping.
// Reporting the clamped pair rather than the arguments is what makes a silently corrected
// configuration visible: a caller that asked for p = 0 can see it got minBloomFPRate.
func (b *Bloom) Capacity() (n int, fp float64) { return b.capacity, b.fpRate }

// SetCreated stamps the construction time carried in the on-disk header. It is a setter rather
// than a NewBloom argument because this package takes no core.Clock: the composition root that
// owns the clock stamps the sketch, and every test that needs a byte-stable frame can pin the
// value without a fake clock.
func (b *Bloom) SetCreated(t core.UnixMilli) { b.created = t }

// FillRatio returns the fraction of bits currently set. It is the quantity §11.4 and §12 both ask
// the operator to monitor, and the input to every saturation decision below.
func (b *Bloom) FillRatio() float64 { return b.fillRatioOf(b.setBits()) }

// EstimatedFPRate returns the filter's current false-positive probability, φ^k, computed from the
// OBSERVED fill ratio φ rather than from the a-priori (1 − e^(−kn/m))^k.
//
// The difference matters for a filter loaded from disk. The a-priori formula is a function of
// Count, which is itself only a lower bound (see Count) and which a forged or truncated header can
// state freely; φ^k is a function of the bits actually set, which is the thing the caller is about
// to be wrong about. A sketch loaded from disk therefore reports the truth about itself without
// having to trust its own metadata — and that is exactly the number §11.4 wants acted on, since
// "the agent starts skipping viable approaches" is a consequence of the bits, not of the count.
func (b *Bloom) EstimatedFPRate() float64 { return b.estFPRateOf(b.FillRatio()) }

// Saturated reports whether the estimated false-positive rate has reached §11.4's watch-for
// threshold of 10%, at which "the agent starts skipping viable approaches". A saturated filter is
// still correct — it never false-negatives — but its answers have stopped being useful, so this is
// the signal to rebuild at a larger capacity.
func (b *Bloom) Saturated() bool { return b.EstimatedFPRate() >= FPWarnRate }

// ResizeTarget reports the capacity and false-positive rate a rebuild should use, and whether one
// is needed at all. needed is true once the fill ratio passes ResizeFillThreshold — which, for the
// Appendix A default, happens at ≈ 9 493 insertions, BEFORE the configured 10 000 is consumed.
// That ordering is the point of §12's mitigation: resizing after saturation is too late.
//
// The growth is capped at MaxBloomCapacity rather than applied blindly. A filter already at the
// ceiling that answered "resize to twice the ceiling" would send its caller into a constructor
// that clamps straight back, i.e. into a rebuild loop that never converges.
func (b *Bloom) ResizeTarget() (capacity int, fp float64, needed bool) {
	return b.resizeTargetFor(b.FillRatio())
}

// BloomStats is a consistent snapshot of everything an observability caller wants to know about a
// filter, taken in a single pass over the bit array. Every field equals what the corresponding
// accessor would return at the moment Stats was called; reading them one accessor at a time would
// cost one popcount pass each.
// The field names and types are the subplan's Interface contract verbatim — SP-09 (negknow) calls
// Stats and SP-14 renders it in /qompack:status, both written against that contract in this same
// wave — so MBits, K and Count int are spelled as the contract spells them rather than as this
// file might otherwise have named them.
type BloomStats struct {
	// Capacity is the configured entry count, after clamping.
	Capacity int
	// FPRate is the configured false-positive rate, after clamping.
	FPRate float64
	// MBits is the bit-array length: the m of Bits().
	MBits uint64
	// K is the number of probes per key: the k of Bits().
	K uint8
	// SetBits is the number of bits currently set — the numerator of FillRatio.
	SetBits uint64
	// Count is the number of Add calls that flipped a bit, saturating at math.MaxInt exactly as
	// Bloom.Count does, so the struct and the accessor cannot disagree.
	Count int
	// FillRatio is the fraction of bits set, FillRatio().
	FillRatio float64
	// EstFPRate is the observed-fill estimate φ^k, EstimatedFPRate().
	EstFPRate float64
	// NeedsResize reports whether FillRatio has passed ResizeFillThreshold (§12). The capacity to
	// rebuild at is ResizeTarget's business; this field is the signal, not the plan.
	NeedsResize bool
	// Saturated reports whether EstFPRate has reached FPWarnRate (§11.4).
	Saturated bool
}

// Stats returns a BloomStats snapshot. It is the one-pass form of FillRatio, EstimatedFPRate,
// Saturated and ResizeTarget's needed flag together: the popcount loop runs once and all four are
// derived from its result, so the fields are guaranteed to describe one instant rather than four
// adjacent ones.
func (b *Bloom) Stats() BloomStats {
	set := b.setBits()
	fill := b.fillRatioOf(set)
	est := b.estFPRateOf(fill)
	_, fp, needed := b.resizeTargetFor(fill)
	return BloomStats{
		Capacity:    b.capacity,
		FPRate:      fp,
		MBits:       b.mBits,
		K:           b.k,
		SetBits:     set,
		Count:       b.Count(),
		FillRatio:   fill,
		EstFPRate:   est,
		NeedsResize: needed,
		Saturated:   est >= FPWarnRate,
	}
}

// setBits is the single popcount pass every saturation figure is derived from.
func (b *Bloom) setBits() uint64 {
	var set uint64
	for _, w := range b.words {
		set += uint64(bits.OnesCount64(w))
	}
	return set
}

// fillRatioOf converts a set-bit count to a fill ratio. The zero Bloom has no bits, and 0/0 is
// NaN, which would propagate through every threshold comparison as a silent false; 0 is the honest
// answer for a filter that holds nothing.
func (b *Bloom) fillRatioOf(set uint64) float64 {
	if b.mBits == 0 {
		return 0
	}
	return float64(set) / float64(b.mBits)
}

// estFPRateOf raises a fill ratio to the k-th power. The k == 0 guard is for the zero Bloom again:
// math.Pow(0, 0) is 1, so an unconstructed filter would otherwise claim a 100% false-positive rate
// and read as permanently saturated.
func (b *Bloom) estFPRateOf(fill float64) float64 {
	if b.k == 0 {
		return 0
	}
	return math.Pow(fill, float64(b.k))
}

// resizeTargetFor is ResizeTarget's body, taking the fill ratio as an argument so Stats can share
// the popcount pass it already paid for.
func (b *Bloom) resizeTargetFor(fill float64) (capacity int, fp float64, needed bool) {
	if fill <= ResizeFillThreshold {
		return b.capacity, b.fpRate, false
	}
	return clampInt(b.capacity*ResizeGrowthFactor, 1, MaxBloomCapacity), b.fpRate, true
}

// Header returns the on-disk metadata for this filter: Kind KindBloom, the current Count, the
// Created stamp, and the four params that make the frame self-describing without consulting
// config. CRC32C is zero, as Sketch documents — the checksum covers the encoded body and is known
// only to DecodeHeader.
func (b *Bloom) Header() Header {
	return Header{
		Magic: HeaderMagic,
		Ver:   FormatVersion,
		Kind:  KindBloom,
		Params: map[string]float64{
			paramCapacity: float64(b.capacity),
			paramFPRate:   b.fpRate,
			paramK:        float64(b.k),
			paramM:        float64(b.mBits),
		},
		Count:   b.count,
		Created: b.created,
	}
}

// MarshalBinary encodes the filter as a QPKS frame: the header above, then m/8 bytes of bit array
// with each word little-endian. EncodeHeader sorts the params, so a given logical state has
// exactly one encoding — which is what makes the frozen golden fixtures and the byte-identical
// re-marshal assertion mean anything.
//
// A nil receiver reports ErrMalformed rather than dereferencing. §12.3: a hook that dies takes
// observability down with it, so every entry point in this package fails by reporting.
//
// An UNSIZED filter — the zero Bloom, reachable as `var b sketch.Bloom` from outside this package
// — is refused for a different reason: the ceiling rule in errors.go says every constructible
// sketch must also be MARSHALLABLE, and marshallable means re-readable, not merely writable. A
// zero Bloom would otherwise emit a structurally valid frame declaring m = 0 and k = 0, which
// decodeV1 then refuses forever. Writing a file that this build's own decoder is guaranteed to
// reject is worse than refusing to write it: the refusal is loud and recoverable at the call site,
// whereas the file is discovered dead on the next restart, which is the one failure §6.2 exists to
// prevent.
func (b *Bloom) MarshalBinary() ([]byte, error) {
	if b == nil {
		return nil, fmt.Errorf("%w: MarshalBinary on a nil *Bloom", ErrMalformed)
	}
	if b.mBits == 0 || b.k == 0 {
		return nil, fmt.Errorf(
			"%w: MarshalBinary on an unsized Bloom (m=%d, k=%d); construct it with NewBloom",
			ErrMalformed, b.mBits, b.k)
	}
	body := make([]byte, len(b.words)*wordBytes)
	for i, w := range b.words {
		binary.LittleEndian.PutUint64(body[i*wordBytes:], w)
	}
	return EncodeHeader(b.Header(), body)
}

// UnmarshalBinary replaces b's entire state with the filter encoded in data, or leaves b untouched
// and reports why it could not.
//
// The version switch is the upgrade path header.go documents: case 1 decodes the layout this build
// writes, and when FormatVersion ever becomes 2 a case 2 arm is added while case 1 stays forever,
// because sketches are permanent memory (§6.2) and the oldest file on disk must still decode.
// DecodeHeader has already refused anything above FormatVersion, so the default arm is reachable
// only if that guarantee is ever broken — which is exactly when a bare sentinel beats a panic.
//
// A nil receiver reports ErrMalformed rather than panicking, for the same reason MarshalBinary
// does.
func (b *Bloom) UnmarshalBinary(data []byte) error {
	if b == nil {
		return fmt.Errorf("%w: UnmarshalBinary on a nil *Bloom", ErrMalformed)
	}
	h, body, err := DecodeHeader(data)
	if err != nil {
		return err
	}
	if h.Kind != KindBloom {
		return fmt.Errorf("%w: frame declares %s, want %s", ErrKindMismatch, h.Kind, KindBloom)
	}
	switch h.Ver {
	case 1:
		return b.decodeV1(h, body)
	default:
		return fmt.Errorf("%w: version %d", ErrUnsupportedVersion, h.Ver)
	}
}

// decodeV1 decodes the FormatVersion 1 layout.
//
// Every param is validated against the same ceilings the constructor clamps to BEFORE a single
// byte is allocated, because a forged header is the whole of this function's threat model: m is
// the length of the make() below, so a frame claiming m = 1e18 has to be refused here rather than
// turned into an out-of-memory kill of the daemon. The body-length check comes last, so a frame
// that is wrong in two ways reports the structural problem (ErrMalformed) rather than the
// downstream length disagreement it causes (ErrTruncated).
func (b *Bloom) decodeV1(h Header, body []byte) error {
	mBits, err := h.MustParamInt(paramM, wordBits, int(MaxBloomBits))
	if err != nil {
		return err
	}
	if mBits%wordBits != 0 {
		return fmt.Errorf("%w: param %q = %d is not a multiple of %d", ErrMalformed, paramM, mBits, wordBits)
	}
	k, err := h.MustParamInt(paramK, 1, maxBloomHashes)
	if err != nil {
		return err
	}
	capacity, err := h.MustParamInt(paramCapacity, 1, MaxBloomCapacity)
	if err != nil {
		return err
	}
	// fprate is not an integer, so MustParamInt cannot carry it. The band is (0, 1) rather than
	// the constructor's [minBloomFPRate, maxBloomFPRate]: a file written by a build with different
	// clamps must still load, and the value is reported, never used to size anything.
	fpRate, ok := h.Param(paramFPRate)
	if !ok {
		return fmt.Errorf("%w: header has no param %q", ErrMalformed, paramFPRate)
	}
	if math.IsNaN(fpRate) || fpRate <= 0 || fpRate >= 1 {
		return fmt.Errorf("%w: param %q = %v is not in (0, 1)", ErrMalformed, paramFPRate, fpRate)
	}
	if len(body) != mBits/bitsPerByte {
		return fmt.Errorf("%w: body of %d bytes cannot hold m = %d bits", ErrTruncated, len(body), mBits)
	}

	// The body is a subslice of the caller's buffer (DecodeHeader), so this copies rather than
	// retaining it.
	words := make([]uint64, mBits/wordBits)
	for i := range words {
		words[i] = binary.LittleEndian.Uint64(body[i*wordBytes:])
	}

	b.mBits = uint64(mBits)
	b.k = uint8(k)
	b.words = words
	b.count = h.Count
	b.capacity = capacity
	b.fpRate = fpRate
	b.created = h.Created
	return nil
}

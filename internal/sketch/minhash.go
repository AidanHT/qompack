package sketch

import (
	"encoding/binary"
	"fmt"
	"math"

	"github.com/qompack/qompack/internal/core"
)

// paramPerms is the on-wire name of the permutation count, the single param a MinHash frame
// carries. It is lower case because the QPKS frame restricts param names to [a-z0-9.] (header.go),
// and it is a constant rather than a literal at three call sites because the encoder and the
// decoder have to agree: a typo in one of them would produce a sketch that saves cleanly and then
// fails to load, which is the one failure §6.2 exists to prevent.
const paramPerms = "perms"

// DefaultShingleSize is the shingle width MinHash uses when MinHashOptions.ShingleSize is unset.
// Eight bytes is long enough that a shingle is a distinctive fragment of a line rather than a
// syllable every document contains, and short enough that a one-line edit invalidates only a
// handful of them — which is what makes "same test suite, one new failure" (§8.1 item 1) still
// score above the near-duplicate threshold.
const DefaultShingleSize = 8

// MinHashSampleTarget is the number of shingles the content-defined sampler aims to keep, whatever
// the input size. It is the cost bound: without it a 1 MiB tool result would cost 1 048 569
// shingles × 128 permutations ≈ 134 million multiply-compares on a hook path §8.1 budgets in
// milliseconds. With it the work is flat in the document size, and the accuracy lost is the
// accuracy of an 8 192-sample estimate — well inside §15's "within 0.1 of exact".
const MinHashSampleTarget = 8192

// MinPermutations is the narrowest signature MinHash will produce. Below 16 permutations the
// standard error of the estimate (1/√P) is worse than 25 %, which is wider than every threshold
// anything downstream compares against, so a smaller signature would not be cheap — it would be
// meaningless.
const MinPermutations = 16

// MaxPermutations is the widest signature MinHash will produce, and the ceiling the decoder
// enforces before it allocates. It bounds one signature at 2 + 8×512 = 4 098 bytes, so a forged
// record field cannot turn into a large allocation, and it is the same [16, 512] range
// 00-ARCHITECTURE.md §11.3 makes config.Validate enforce — applied again here because a
// hand-edited config must not be able to produce a Signature this package could not read back.
const MaxPermutations = 512

// The shingle-width bounds. They are not exported because nothing outside this package chooses a
// shingle width except through MinHashOptions, and both ends exist to keep the estimator
// meaningful rather than to bound an allocation: at w = 1 every document drawn from the same
// alphabet looks alike, and at w = 4096 every document shorter than a page has no shingles at all
// and §8.1's near-duplicate detection silently stops working.
const (
	// minShingleSize is the narrowest shingle a caller can ask for.
	minShingleSize = 2
	// maxShingleSize is the widest shingle a caller can ask for.
	maxShingleSize = 64
)

// The compact form's field widths (see Signature.MarshalBinary for the layout).
const (
	// sigPermsLen is the width of the leading uint16 permutation count.
	sigPermsLen = 2
	// sigMinLen is the encoded width of one minimum.
	sigMinLen = 8
)

// MinHashOptions configures MinHash signature computation (00-ARCHITECTURE.md §5.7, Qompack.md
// §8.1). canon (SP-04) carries the whole struct from config to the call site, which is why it
// holds a field this package never reads — see NearDupThreshold.
type MinHashOptions struct {
	// Enabled turns near-duplicate detection on or off. When false MinHash returns the zero
	// Signature, which every comparison then refuses.
	Enabled bool
	// Permutations is the number of MinHash permutation functions, clamped to
	// [MinPermutations, MaxPermutations].
	Permutations int
	// ShingleSize is the shingle length, in bytes, MinHash hashes over. Zero and negative values
	// mean DefaultShingleSize; anything else is clamped to [2, 64].
	ShingleSize int
	// NearDupThreshold is the Jaccard similarity above which two signatures are treated as
	// near-duplicates. It is read by NO code in this package: the policy belongs to SP-04 and
	// SP-06, which pass the value to Signature.IsNearDup explicitly.
	NearDupThreshold float64
}

// Signature is a MinHash signature over canonicalized content, embedded in canon.Result (SP-04) and
// in store.ToolUseRecord (SP-06) to support near-duplicate detection.
//
// Perms and len(Mins) always agree on a Signature this package produced; the pair is validated on
// both sides of every comparison and of every codec call, because a Signature is also constructible
// by hand and decodable from a record field, and Jaccard would otherwise index past the end of a
// short Mins.
//
// The zero Signature (Perms 0, Mins nil) means "MinHash was disabled". It is deliberately NOT the
// same value as the empty-document signature, which is Perms copies of math.MaxUint64: one says
// nothing was measured, the other says nothing was found, and a consumer deciding whether it holds
// a comparable value has to be able to tell them apart.
type Signature struct {
	// Perms is the number of permutation functions Mins was computed with.
	Perms uint16
	// Mins holds one minimum hash value per permutation.
	Mins []uint64
}

// MinHash returns the k-permutation MinHash signature of data (00-ARCHITECTURE.md §5.7, Qompack.md
// §8.1 item 1). The seven steps are:
//
//  1. A disabled option set returns the zero Signature; nothing else is computed.
//  2. The shingle width is o.ShingleSize, or DefaultShingleSize when unset, clamped to [2, 64].
//  3. The permutation count is o.Permutations clamped to [MinPermutations, MaxPermutations], with
//     clampInt rather than clamp so an int never round-trips through float64 (util.go).
//  4. A document with no shingles returns P copies of math.MaxUint64 — the canonical
//     empty-document signature, which compares as Jaccard 1 against another empty document because
//     their shingle sets are both empty and therefore identical.
//  5. Shingles are sampled by CONTENT: a shingle is kept iff fnv1a64(shingle) ≤ MaxUint64/rate,
//     where rate is chosen to keep about MinHashSampleTarget of them. If a pass keeps nothing —
//     which a repetitive document can do, because the rate is derived from the number of shingle
//     positions and selection applies to the distinct shingles — the rate is halved and the pass
//     runs again. See minHashWithStats for why that retry is a correction to the plan.
//  6. Each kept hash h is permuted as v = a[i]·h + b[i] in wrapping uint64 arithmetic, with
//     a[i] = splitmix64(2i)|1 and b[i] = splitmix64(2i+1), and the running minimum is kept per i.
//  7. The result is Signature{Perms: P, Mins: mins}.
//
// # Why the sampler is content-defined and not positional
//
// Taking every rate-th shingle would be cheaper to write and would destroy the estimator. An
// insertion shifts the POSITION of every subsequent byte, so a positional sampler would select an
// entirely different subset of the tail and report two documents differing by one line as
// unrelated — which is precisely the case §8.1 item 1 needs to detect. Selecting on the shingle's
// own hash makes the decision a function of content alone: the unaffected shingles of an edited
// document are sampled exactly as they were before the edit.
//
// # Why fnv1a64 and the coefficient derivation are frozen
//
// Signatures are persisted in an append-only index (store.ToolUseRecord.Signature). Changing the
// shingle hash, the coefficient derivation or the permutation makes every stored signature
// incomparable with every new one, silently, because both sides still look like well-formed
// signatures. TestMinHash_StableAcrossRuns pins the first four minima of a frozen fixture for
// exactly that reason.
//
// o.NearDupThreshold is not read here or anywhere else in this package: the near-duplicate POLICY
// belongs to SP-04 and SP-06, which pass the threshold to Signature.IsNearDup explicitly.
//
// MinHash is pure and safe for concurrent use; store (SP-06) calls it from its worker pool.
func MinHash(data []byte, o MinHashOptions) Signature {
	sig, _ := minHashWithStats(data, o)
	return sig
}

// minHashWithStats is MinHash's body, plus the number of shingles the sampler actually kept.
//
// The count exists so TestMinHash_SubsamplingEngages can assert the cost bound DIRECTLY — a
// statement about the algorithm — instead of inferring it from a wall-clock reading, which would be
// a statement about the machine the suite happens to run on. It is unexported because it is a
// testing affordance and not part of §5.7's surface.
func minHashWithStats(data []byte, o MinHashOptions) (Signature, int) {
	if !o.Enabled {
		return Signature{}, 0
	}

	w := o.ShingleSize
	if w <= 0 {
		w = DefaultShingleSize
	}
	w = clampInt(w, minShingleSize, maxShingleSize)
	p := clampInt(o.Permutations, MinPermutations, MaxPermutations)

	// MaxUint64 is the identity for "no minimum seen yet", and it is also the answer for a document
	// with no shingles at all, so the fill serves both the loop below and step 4.
	mins := make([]uint64, p)
	for i := range mins {
		mins[i] = math.MaxUint64
	}

	nsh := len(data) - w + 1
	if nsh <= 0 {
		return Signature{Perms: uint16(p), Mins: mins}, 0
	}

	// Both coefficient tables come out of ONE allocation — 8 KB at the 512-permutation ceiling —
	// because they are computed per call rather than held in a package-level table: this package
	// refuses package-level mutable state, and a table shared across goroutines would have to be
	// either immutable-by-convention or locked, on the hot path of every store write.
	coef := make([]uint64, 2*p)
	a, b := coef[:p:p], coef[p:]
	for i := range a {
		a[i] = splitmix64(uint64(2*i)) | 1
		b[i] = splitmix64(uint64(2*i + 1))
	}
	// a, b and mins are all exactly p long. Re-slicing the other two to len(a) here, once, is what
	// lets the compiler drop the bounds checks from the innermost loop below — which runs
	// MinHashSampleTarget × P times per call and is where essentially all of the cost is.
	b, mins = b[:len(a)], mins[:len(a)]

	// Step 5, with the plan's algorithm CORRECTED. keep is the inclusive ceiling a shingle's hash
	// must fall under to be sampled; rate 1 leaves it at MaxUint64, so a document at or under the
	// target is measured in full and loses nothing to a sampler it never needed. fnv1a64's output
	// is uniform over uint64, so the ceiling keeps about 1/rate of the DISTINCT shingles.
	//
	// # Why the rate retries downward
	//
	// The rate is derived from nsh, the number of shingle POSITIONS, but selection applies to
	// distinct shingle HASHES, and on repetitive input the two diverge without limit. A 1 MiB build
	// log that is one 60-byte line repeated has nsh ≈ 1 048 569 and therefore rate 128, but only
	// ~60 distinct shingles, each surviving with probability 1/128: better than even odds that
	// NOTHING survives. The plan's algorithm (lines 918 and 943 of the subplan) never considers
	// kept == 0 with nsh > 0, so this corrects it rather than deviating from it.
	//
	// The consequence is not inaccuracy, it is a wrong answer of the worst available kind: every
	// minimum stays MaxUint64, which is byte-identical to the canonical empty-document signature of
	// step 4. The document would then report Jaccard 1 against an empty document, against "abc",
	// and against every other document that also sampled to nothing — and SP-06 uses exactly that
	// answer to store a delta in place of the text. A build log of thousands of identical lines is
	// an ordinary tool result, so this is reachable rather than theoretical.
	//
	// Halving and selecting again fixes it at negligible cost. The retry only ever fires when the
	// distinct count is tiny relative to nsh, so the permutation work stays proportional to the
	// distinct count — a handful of shingles × P — rather than to nsh; the price is at most
	// log2(rate) extra FNV passes over the document at roughly 5 ns a shingle. Selection remains a
	// pure function of content AT EVERY RATE: falling back to "keep the first N positions", or to
	// any other positional rule, would trade this bug for a shift-invariance bug, which is the one
	// failure this sampler exists to prevent.
	//
	// The loop is bounded by rate reaching 0 rather than by a claim about fnv1a64's range. In
	// practice it cannot get that far — at rate 1 the ceiling is MaxUint64, every shingle passes
	// and kept is nsh ≥ 1 — so the bound is a structural guarantee for a reader who should not have
	// to re-derive that argument, not a case anything can reach.
	kept := 0
	for rate := (nsh + MinHashSampleTarget - 1) / MinHashSampleTarget; rate >= 1; rate /= 2 {
		keep := uint64(math.MaxUint64)
		if rate > 1 {
			keep = math.MaxUint64 / uint64(rate)
		}
		for i := 0; i < nsh; i++ {
			h := fnv1a64(data[i : i+w])
			if h > keep {
				continue
			}
			kept++
			// Wrapping arithmetic is the point, not an accident: a·h + b over the full uint64 ring
			// is the standard universal permutation family, and Go's defined wraparound is what
			// makes it reproducible on every architecture this ships to.
			for j, aj := range a {
				if v := aj*h + b[j]; v < mins[j] {
					mins[j] = v
				}
			}
		}
		if kept > 0 {
			break
		}
		// Nothing was kept, so no minimum was lowered: mins is still the all-MaxUint64 array step 4
		// filled, and the next pass starts from exactly the state this one did. The retry is
		// idempotent for that reason, and needs no reset.
	}
	return Signature{Perms: uint16(p), Mins: mins}, kept
}

// Jaccard estimates the Jaccard similarity of the two documents behind s and o, as the fraction of
// permutation positions whose minima agree. That fraction is an unbiased estimator of the true set
// Jaccard with a standard error of 1/√Perms — 8.8 % at Appendix C's 128 permutations, comfortably
// inside §15's "within 0.1 of exact on known sets".
//
// Signatures of different widths, and the zero signature, are INCOMPARABLE and yield 0. Two widths
// cannot be compared position by position because position i of a 128-wide signature and position i
// of a 256-wide one are minima of the same permutation, so a partial comparison over the shared
// prefix would score unrelated documents far too high — a fabricated answer, where 0 is an honest
// one.
//
// Both operands' Mins are checked against both declared widths, not just the receiver's. The loop
// ranges over s.Mins and indexes o.Mins, so a signature whose Perms exceeds its Mins would panic an
// implementation that trusted Perms — and it is exactly the value a forged record field decodes to.
// Checking both is also what makes Jaccard symmetric on those inputs.
//
// Jaccard is pure and safe for concurrent use.
func (s Signature) Jaccard(o Signature) float64 {
	if s.Perms == 0 || s.Perms != o.Perms || len(s.Mins) != int(s.Perms) || len(o.Mins) != int(o.Perms) {
		return 0
	}
	eq := 0
	for i := range s.Mins {
		if s.Mins[i] == o.Mins[i] {
			eq++
		}
	}
	return float64(eq) / float64(s.Perms)
}

// IsNearDup reports whether s and o are near-duplicates at the given threshold — §8.1 item 1's
// "same test suite, one new failure", which SP-06 answers by storing a delta against the prior
// version instead of the full text.
//
// The threshold is a parameter rather than MinHashOptions.NearDupThreshold read from a struct,
// because the policy belongs to the caller: canon (SP-04) and store (SP-06) each pass the
// configured value at the point of decision, so there is one place to look for what "near" means.
//
// The Perms != 0 guard is not redundant with Jaccard's. Jaccard reports 0 for the zero signature,
// and 0 >= 0.0 is true, so without this test a configured threshold of 0 would make the zero
// signature a near-duplicate of everything — including of itself — and a document whose MinHash was
// disabled would be dropped as a duplicate of the first thing it was compared against.
//
// IsNearDup is pure and safe for concurrent use.
func (s Signature) IsNearDup(o Signature, threshold float64) bool {
	return s.Perms != 0 && s.Perms == o.Perms && s.Jaccard(o) >= threshold
}

// MarshalBinary encodes s in the compact form, which carries NO QPKS header because the store
// record line that holds it is already framed and checksummed:
//
//	offset  size          field
//	0       2             Perms   uint16, little-endian
//	2       8×Perms       Mins    little-endian uint64s
//
// A signature whose Perms and Mins disagree, or whose Perms is past MaxPermutations, is refused
// rather than written. Neither is producible by MinHash, but both are constructible by hand, and
// writing one would put bytes into an append-only index that this package's own decoder then
// refuses — the same "every writable sketch must be readable" rule errors.go states for the framed
// forms.
//
// MarshalBinary is pure and safe for concurrent use.
func (s Signature) MarshalBinary() ([]byte, error) {
	if len(s.Mins) != int(s.Perms) {
		return nil, fmt.Errorf("%w: signature declares %d permutations but carries %d minima",
			ErrMalformed, s.Perms, len(s.Mins))
	}
	if int(s.Perms) > MaxPermutations {
		return nil, fmt.Errorf("%w: %d permutations exceeds MaxPermutations (%d)",
			ErrMalformed, s.Perms, MaxPermutations)
	}
	b := make([]byte, sigPermsLen, sigPermsLen+sigMinLen*len(s.Mins))
	binary.LittleEndian.PutUint16(b, s.Perms)
	for _, m := range s.Mins {
		b = binary.LittleEndian.AppendUint64(b, m)
	}
	return b, nil
}

// UnmarshalBinary replaces s with the signature encoded in data, or leaves s untouched and reports
// why it could not.
//
// The threat model is a forged record field, so Perms — which is both a declared length and an
// allocation size — is validated against MaxPermutations BEFORE anything is allocated, and the
// length is then required to match 2 + 8×Perms EXACTLY rather than merely to be large enough. The
// order is deliberate: the range check precedes the length check so that an absurd width reports
// what is actually wrong with it (ErrMalformed) rather than the length disagreement it causes.
//
// A zero-length input is ErrTruncated, never a silently empty signature. A record whose signature
// field was lost must not decode into "MinHash was disabled", because that reads as a deliberate
// choice rather than as damage.
//
// Every rejection returns a BARE sentinel rather than a fmt.Errorf wrap, for the reason
// DecodeHeader does: wrapping allocates, and TestSignature_UnmarshalRejects holds the oversize path
// to zero allocations — which is what makes the MaxPermutations check a bound rather than a
// comment.
func (s *Signature) UnmarshalBinary(data []byte) error {
	if s == nil {
		return ErrMalformed
	}
	if len(data) < sigPermsLen {
		return ErrTruncated
	}
	perms := binary.LittleEndian.Uint16(data)
	if int(perms) > MaxPermutations {
		return ErrMalformed
	}
	if len(data) != sigPermsLen+sigMinLen*int(perms) {
		return ErrTruncated
	}
	// Perms == 0 decodes to the zero Signature — nil Mins, not an empty slice — so that the
	// disabled signature round-trips to the value MinHash produces for a disabled option set.
	if perms == 0 {
		*s = Signature{}
		return nil
	}

	// data is the caller's buffer (and, under SigSketch, a subslice of it), so this copies rather
	// than retaining it.
	mins := make([]uint64, perms)
	for i := range mins {
		off := sigPermsLen + sigMinLen*i
		mins[i] = binary.LittleEndian.Uint64(data[off : off+sigMinLen])
	}
	s.Perms = perms
	s.Mins = mins
	return nil
}

// SigSketch wraps a Signature so that it satisfies Sketch, which is what makes KindMinHash
// reachable through the same CRC-checked container as the other four sketch types (Save/Load).
//
// It exists because the compact form has no checksum of its own — the store record line that
// normally carries a Signature is checksummed instead — so a signature written as a standalone FILE
// would have no way to detect bit rot. SP-16's per-segment work needs exactly that framed on-disk
// form.
//
// SigSketch is NOT safe for concurrent use: UnmarshalBinary replaces Sig wholesale. The daemon
// (SP-05) owns every live sketch behind its session-registry mutex. The Signature it carries, and
// every method on it, remain pure.
type SigSketch struct {
	// Sig is the wrapped signature.
	Sig Signature
	// Created is when the signature was computed; it is stamped by the caller rather than read
	// from a clock, so that marshalled bytes are byte-stable in golden tests.
	Created core.UnixMilli
}

// Header returns the on-disk metadata for this sketch: Kind KindMinHash, the signature width as
// both Count and the single param, and the Created stamp. Count is the width rather than a number
// of items added because a signature is not accumulated — it is computed once from a whole
// document — and reporting the width twice makes the frame self-describing to a reader that has
// only the header. CRC32C is zero, as Sketch documents.
func (s *SigSketch) Header() Header {
	return Header{
		Magic:   HeaderMagic,
		Ver:     FormatVersion,
		Kind:    KindMinHash,
		Params:  map[string]float64{paramPerms: float64(s.Sig.Perms)},
		Count:   uint64(s.Sig.Perms),
		Created: s.Created,
	}
}

// MarshalBinary encodes the sketch as a QPKS frame whose body is the compact form verbatim. An
// inconsistent Signature is refused by Signature.MarshalBinary before any frame is assembled.
//
// A nil receiver reports ErrMalformed rather than dereferencing. §12.3: a hook that dies takes
// observability down with it, so every entry point in this package fails by reporting.
func (s *SigSketch) MarshalBinary() ([]byte, error) {
	if s == nil {
		return nil, fmt.Errorf("%w: MarshalBinary on a nil *SigSketch", ErrMalformed)
	}
	body, err := s.Sig.MarshalBinary()
	if err != nil {
		return nil, err
	}
	return EncodeHeader(s.Header(), body)
}

// UnmarshalBinary replaces s's entire state with the sketch encoded in data, or leaves s untouched
// and reports why it could not.
//
// The version switch is the upgrade path header.go documents: case 1 decodes the layout this build
// writes, and when FormatVersion ever becomes 2 a case 2 arm is added while case 1 stays forever,
// because sketches are permanent memory (§6.2) and the oldest file on disk must still decode.
// DecodeHeader has already refused anything above FormatVersion, so the default arm is reachable
// only if that guarantee is ever broken — which is exactly when a bare sentinel beats a panic.
//
// A nil receiver reports ErrMalformed rather than panicking, for the same reason MarshalBinary does.
func (s *SigSketch) UnmarshalBinary(data []byte) error {
	if s == nil {
		return fmt.Errorf("%w: UnmarshalBinary on a nil *SigSketch", ErrMalformed)
	}
	hdr, body, err := DecodeHeader(data)
	if err != nil {
		return err
	}
	if hdr.Kind != KindMinHash {
		return fmt.Errorf("%w: frame declares %s, want %s", ErrKindMismatch, hdr.Kind, KindMinHash)
	}
	switch hdr.Ver {
	case 1:
		return s.decodeV1(hdr, body)
	default:
		return fmt.Errorf("%w: version %d", ErrUnsupportedVersion, hdr.Ver)
	}
}

// decodeV1 decodes the FormatVersion 1 layout.
//
// The param is read through MustParamInt against [0, MaxPermutations] first, so a forged width is
// refused by the same bound the constructor obeys. The body is then decoded — Signature's own
// decoder re-checks the width and the exact length before it allocates — and the two are
// cross-checked against each other: a frame whose header says 128 and whose body says 4 is
// internally inconsistent, and silently trusting one of the two would make Count and Params lie
// about the signature a reader just loaded.
func (s *SigSketch) decodeV1(hdr Header, body []byte) error {
	perms, err := hdr.MustParamInt(paramPerms, 0, MaxPermutations)
	if err != nil {
		return err
	}
	var sig Signature
	if err := sig.UnmarshalBinary(body); err != nil {
		return err
	}
	if int(sig.Perms) != perms {
		return fmt.Errorf("%w: header declares %d permutations, body carries %d",
			ErrMalformed, perms, sig.Perms)
	}

	s.Sig = sig
	s.Created = hdr.Created
	return nil
}

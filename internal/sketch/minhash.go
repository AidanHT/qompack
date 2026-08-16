package sketch

import (
	"encoding/binary"
	"fmt"
	"math"
	"math/bits"

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

// MinHashSampleTarget is the number of DISTINCT shingle hashes a signature is computed over: MinHash
// keeps the MinHashSampleTarget smallest of them and permutes only those. It is the cost bound —
// without it a 1 MiB tool result would cost 1 048 569 shingles × 128 permutations ≈ 134 million
// multiply-compares on a path §8.1 budgets in milliseconds — and with it the permutation work is
// flat in the document size, at the accuracy of an 8 192-sample estimate, well inside §15's "within
// 0.1 of exact".
//
// It is a COUNT and not a sampling-rate target, and the distinction is the whole of Ruling MH2. The
// sampler that shipped first derived a rate ⌈nsh/target⌉ from the shingle count and kept every
// distinct hash below MaxUint64/rate. That made the keep-threshold a STEP function of document
// length: two near-identical documents whose shingle counts straddled a multiple of this constant
// were sampled at densities differing by a whole integer factor, so for each permutation their
// minima agreed with probability ≈ 1/rate however similar the documents were — 0.45 estimated
// against a true Jaccard of 0.98 at the first boundary, and a boundary every 8 KB thereafter, across
// the whole size range of an ordinary tool result. Keeping the smallest target hashes instead makes
// the effective threshold the target-th smallest distinct hash, which moves CONTINUOUSLY with the
// document, so two documents of similar size get near-identical thresholds and their estimates are
// comparable. TestMinHash_StraddlingTheSampleTargetIsContinuous walks the first eight boundaries.
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

// The bottom-k selector's working sizes. All three are stated relative to MinHashSampleTarget rather
// than as absolute numbers, so that the target is the single place a reader has to look to reason
// about what the sampler costs.
const (
	// shingleChunk is how many shingle hashes are computed before any of them is looked at. The
	// batch exists for one reason and it is worth a sentence, because it looks like pointless
	// indirection: it puts the FNV loop in a function whose shingle width is a COMPILE-TIME constant
	// on the common path (hashShingles), which lets the compiler unroll the eight-step multiply
	// chain and overlap consecutive shingles. Measured on the development host, that is the
	// difference between 580 µs and 323 µs for the 102 393 shingles of a 100 KiB document — a 257 µs
	// saving against that row's committed 1.381 ms, so hashing is about a quarter of MinHash's cost
	// once batched, not the largest share. permuteInto is (see its doc comment). Interleaving the two
	// would serialise them, which is the second reason for the batch.
	// 1 024 hashes is 8 KiB, small enough to stay in L1 between the two passes.
	shingleChunk = 1 << 10
	// sampleSeedSlack sets the selector's FIRST keep-threshold at slack×target/nsh of the hash
	// range, instead of admitting everything and narrowing from there. fnv1a64's output is uniform,
	// so a document whose shingles are mostly distinct admits about slack×target of them and is
	// selected in one pass over a buffer that never grows. A slack of 2 tolerates an average shingle
	// multiplicity of 2 — ordinary prose, code and log output — before the seed under-admits and
	// bottomKShingles re-runs the general pass. See bottomKShingles for why that re-run cannot
	// change the answer.
	sampleSeedSlack = 2
	// sampleBufferSlack sets the dense buffer at slack×target values. It matches sampleSeedSlack
	// because the buffer's job is to hold what the seed admits — but note what that equality does
	// NOT buy: the seed's expected yield on an all-distinct document is exactly slack×target, which
	// is the capacity, so such a document overflows and trims about half the time rather than
	// sailing through. That is a cost, not a bug: the trim is one selectKth over 16 384 values and
	// cannot change the answer. Sizing the buffer above the seed's yield would buy those trims back
	// at the price of the property that actually matters here — the buffer, and therefore MinHash's
	// whole working set above the target, is bounded at 2 × 8 192 × 8 B = 128 KiB no matter how large
	// the document is.
	sampleBufferSlack = 2
	// shingleHashBits is the width of a shingle hash, which is what a slot index is carved out of.
	// It is named here rather than borrowed from hll.go's rankHashBits because the two are the same
	// number about different things, and a future change to either must not silently move the other.
	shingleHashBits = 64
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
//  5. Shingles are sampled by CONTENT, as bottom-k over their hashes: the MinHashSampleTarget
//     SMALLEST DISTINCT values of fnv1a64(shingle) are kept, and a document with fewer distinct
//     shingle hashes than that keeps all of them. A document at or under the target is measured in
//     full and no selection runs at all.
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
// # Why bottom-k, and why the empty signature is now structurally impossible
//
// Bottom-k is what makes the selection comparable ACROSS documents as well as invariant within one.
// The effective keep-threshold is the target-th smallest distinct hash, which is a continuous
// function of the document; the rate-based rule this replaced stepped that threshold by a whole
// integer factor at every multiple of MinHashSampleTarget, and two near-identical documents
// straddling such a boundary were then compared at incompatible sampling densities — see
// MinHashSampleTarget for the measured size of that failure.
//
// It also removes a defensive case rather than handling it. Under the rate-based rule a repetitive
// document — a megabyte of one repeated build-log line has ~58 distinct 8-shingles among 1 048 569
// positions — could keep NOTHING, because the rate was derived from positions while selection
// applied to distinct hashes; every minimum then stayed MaxUint64, byte-identical to the
// empty-document signature of step 4, so the document reported Jaccard 1 against an empty one and
// SP-06 would store a delta in place of the text. Ruling MH1 answered that by halving the rate and
// retrying. Under bottom-k the case cannot arise: the smallest k distinct values of a non-empty set
// are a non-empty set, so a document with at least one shingle always keeps at least one hash. The
// retry is gone rather than left in place unreachable.
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

// minHashWithStats is MinHash's body, plus the number of shingle hashes it permuted.
//
// The count exists so TestMinHash_SubsamplingEngages can assert the cost bound DIRECTLY — a
// statement about the algorithm — instead of inferring it from a wall-clock reading, which would be
// a statement about the machine the suite happens to run on. It is unexported because it is a
// testing affordance and not part of §5.7's surface.
//
// The count means what it says and the two branches differ, which is worth stating because it looks
// like an inconsistency: above MinHashSampleTarget it is the number of DISTINCT hashes the selector
// kept, and at or under the target it is the number of shingle POSITIONS, because no selection runs
// there and a repeated minimum is idempotent. Both are "how many hashes went through the
// permutation loop", which is the quantity the cost bound is about.
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

	// Step 5. Above the target the selector runs and only the surviving hashes are permuted; at or
	// under it there is nothing to select, so the document is measured in full and loses no accuracy
	// to a selection it never needed. Splitting the two is not just an optimisation: the small case
	// is every golden fixture and every frozen expectation in this package, and keeping it a plain
	// pass over every shingle is what makes "bottom-k changed nothing below the target" a property
	// of the code rather than of an argument about it.
	if nsh > MinHashSampleTarget {
		sample := bottomKShingles(data, w, nsh)
		permuteInto(mins, a, b, sample)
		return Signature{Perms: uint16(p), Mins: mins}, len(sample)
	}

	var chunk [shingleChunk]uint64
	for off := 0; off < nsh; off += shingleChunk {
		batch := chunk[:min(shingleChunk, nsh-off)]
		hashShingles(batch, data, off, w)
		permuteInto(mins, a, b, batch)
	}
	return Signature{Perms: uint16(p), Mins: mins}, nsh
}

// permuteInto lowers each of mins to the smallest a[j]·h + b[j] seen over hashes — step 6.
//
// Wrapping arithmetic is the point, not an accident: a·h + b over the full uint64 ring is the
// standard universal permutation family, and Go's defined wraparound is what makes it reproducible
// on every architecture this ships to.
//
// a, b and mins are all exactly P long. Re-slicing the other two to len(a) here, once per batch, is
// what lets the compiler drop the bounds checks from the inner loop — which runs up to
// MinHashSampleTarget × P times per signature and is the single largest share of MinHash's cost:
// about 0.81 ms of the 100 KiB row's committed 1.381 ms, against ~0.32 ms of shingle hashing and
// ~0.25 ms of selection. Optimising this loop is worth roughly three times optimising either.
func permuteInto(mins, a, b, hashes []uint64) {
	b, mins = b[:len(a)], mins[:len(a)]
	for _, h := range hashes {
		for j, aj := range a {
			if v := aj*h + b[j]; v < mins[j] {
				mins[j] = v
			}
		}
	}
}

// hashShingles fills dst with fnv1a64 of the w-byte shingle at each of data[off], data[off+1], …
//
// The default width is spelled as a CONSTANT in its own loop, and that is the whole reason this
// function exists as a batch rather than as a call per shingle inside the sampler. At a constant
// width the compiler knows the shingle is eight bytes long, unrolls FNV-1a's multiply chain and
// overlaps consecutive shingles; at a variable width it emits a real loop whose iterations cannot
// overlap. The two forms produce identical hashes — TestMinHash_BottomKMatchesTheSlowDefinition
// checks the non-default widths against the same reference the default one is checked against — and
// on the development host the constant-width form measures 323 µs against 580 µs for the 102 393
// shingles of a 100 KiB document.
func hashShingles(dst []uint64, data []byte, off, w int) {
	if w == DefaultShingleSize {
		for i := range dst {
			dst[i] = fnv1a64(data[off+i : off+i+DefaultShingleSize])
		}
		return
	}
	for i := range dst {
		dst[i] = fnv1a64(data[off+i : off+i+w])
	}
}

// bottomKShingles returns the MinHashSampleTarget smallest DISTINCT shingle hashes of data, or every
// distinct hash when the document has fewer than that. It is only called with nsh above the target.
//
// The mechanism is a threshold estimate with a refinement pass. fnv1a64's output is uniform over
// uint64, so a keep-threshold of (sampleSeedSlack × target / nsh) of the range admits about
// sampleSeedSlack × target shingle hashes — enough to contain the target smallest ones with room to
// spare — and the selection then finishes inside a buffer that never has to grow. Starting instead
// from "admit everything and narrow", which is the obvious way to write it, costs a run of
// increasingly narrow trims over the whole document and measured 2.87 ms against a 2.5 ms budget for
// the 100 KiB case.
//
// The seed is an OPTIMISATION and cannot change the answer, which is what separates this re-run from
// the halving retry Ruling MH1 needed. If at least target distinct hashes fall below the seed, then
// the target-th smallest distinct hash of the whole document is one of them and the pass is exact.
// If fewer do — a document whose shingles repeat more than sampleSeedSlack times on average — the
// general pass runs with no threshold at all and returns what it always would have. Both paths
// return the same set; only the cost differs.
func bottomKShingles(data []byte, w, nsh int) []uint64 {
	keep := uint64(math.MaxUint64)
	// (MaxUint64/nsh)×admit cannot overflow, because admit < nsh. It is integer arithmetic rather
	// than a float ratio so the threshold is exactly reproducible on every platform this ships to:
	// the sampler's output is persisted in an append-only index, and a rounding difference between
	// two builds would move which shingles a document keeps.
	if admit := sampleSeedSlack * MinHashSampleTarget; admit < nsh {
		keep = math.MaxUint64 / uint64(nsh) * uint64(admit)
	}
	sample := distinctBelow(data, w, nsh, keep)
	if len(sample) < MinHashSampleTarget && keep != math.MaxUint64 {
		sample = distinctBelow(data, w, nsh, math.MaxUint64)
	}
	return sample
}

// distinctBelow returns the MinHashSampleTarget smallest distinct shingle hashes at or below keep,
// or all of them when fewer than that many exist below it.
//
// One pass, three structures, and each earns its place. shingleSet rejects a hash already seen, so
// the buffer only ever holds distinct values — without it a repetitive document would fill the
// buffer with copies of its smallest hash and the "smallest k DISTINCT" rule would silently become
// "smallest k positions", which for a build log of one repeated line degenerates to a single hash.
// The dense buffer is what selectKth can work over. And keep is lowered to the target-th smallest
// value whenever the buffer fills, which is what bounds the pass: each trim halves the admission
// rate, so the buffer cannot fill more than log2(nsh/target) times however long the document is.
//
// Memory is bounded by the buffer rather than by the document: 128 KiB of values and 256 KiB of set
// slots, for a 4 KiB tool result and a 64 MiB one alike.
func distinctBelow(data []byte, w, nsh int, keep uint64) []uint64 {
	const capacity = sampleBufferSlack * MinHashSampleTarget

	seen := newShingleSet(capacity)
	sample := make([]uint64, 0, capacity)
	var chunk [shingleChunk]uint64

	for off := 0; off < nsh; off += shingleChunk {
		batch := chunk[:min(shingleChunk, nsh-off)]
		hashShingles(batch, data, off, w)
		for _, h := range batch {
			if h > keep || !seen.add(h) {
				continue
			}
			sample = append(sample, h)
			if len(sample) < capacity {
				continue
			}
			keep = selectKth(sample, MinHashSampleTarget-1)
			sample = sample[:MinHashSampleTarget]
			seen.reset()
			for _, v := range sample {
				seen.add(v)
			}
		}
	}
	// The buffer holds between MinHashSampleTarget and capacity values on any path that filled it,
	// and fewer than MinHashSampleTarget on a document that never did; only the first needs a final
	// trim, and the second is the "keep them all" case.
	if len(sample) > MinHashSampleTarget {
		selectKth(sample, MinHashSampleTarget-1)
		sample = sample[:MinHashSampleTarget]
	}
	return sample
}

// selectKth partially orders a so that a[k] holds the (k+1)-th smallest value and every element
// before it is no larger, and returns that value. It is quickselect with a median-of-three pivot:
// linear in len(a) on average, against the n·log n a sort would cost for an ordering nothing here
// needs. Sorting the 16 384-value buffer instead measured 8.2 ms for the 100 KiB case against a
// 2.5 ms budget.
//
// Only the "everything before k is no larger" half of the postcondition is used — distinctBelow
// truncates to a[:k+1] and never reads the order — but both halves are stated because a caller that
// assumed a[:k+1] came out sorted would be wrong.
func selectKth(a []uint64, k int) uint64 {
	lo, hi := 0, len(a)-1
	for lo < hi {
		pivot := medianOfThree(a, lo, hi)
		l, r := lo, hi
		for l <= r {
			for a[l] < pivot {
				l++
			}
			for a[r] > pivot {
				r--
			}
			if l <= r {
				a[l], a[r] = a[r], a[l]
				l++
				r--
			}
		}
		switch {
		case k <= r:
			hi = r
		case k >= l:
			lo = l
		default:
			// k landed between the two partitions, which happens only when a[k] already equals the
			// pivot: every element before it is no larger and the search is over.
			return a[k]
		}
	}
	return a[k]
}

// medianOfThree returns the median of a[lo], a[hi] and the element between them. A middle-element
// pivot alone is quadratic on an already-partitioned array, which is exactly what distinctBelow
// hands this function on its second and later trims.
func medianOfThree(a []uint64, lo, hi int) uint64 {
	x, y, z := a[lo], a[lo+(hi-lo)/2], a[hi]
	if x > y {
		x, y = y, x
	}
	if y > z {
		y = z
	}
	if x > y {
		y = x
	}
	return y
}

// shingleSetMix is the multiplier that turns a shingle hash into a slot index (Fibonacci hashing:
// 2^64/φ, rounded to an odd integer — the same constant splitmix64 opens with).
//
// The mix is not optional and the reason is specific to this caller. Every value the set holds is at
// or below distinctBelow's keep threshold, so they all share a run of leading zero bits; indexing on
// the high bits of the hash itself would pile every one of them into the same corner of the table.
// Indexing on the LOW bits would be no better, since FNV-1a's final step is a multiply and its low
// bits are the least mixed part of its output. Multiplying and taking the high bits of the product
// makes every slot index depend on every bit of the hash.
const shingleSetMix = 0x9E3779B97F4A7C15

// shingleSet is an open-addressed set of shingle hashes with linear probing, sized once at
// construction and never grown. It exists rather than a map[uint64]struct{} because it is probed
// once per admitted shingle on a path budgeted in milliseconds: a Go map costs a hash, a bucket
// walk and an interface-free but still indirect lookup, where this is one multiply and a load that
// usually hits.
//
// It is NOT safe for concurrent use, and nothing shares one: distinctBelow builds one per call, on
// the stack of a pure function.
type shingleSet struct {
	// slots holds the members, with 0 meaning empty.
	slots []uint64
	// shift turns a mixed hash into a slot index: 64 − log2(len(slots)).
	shift uint
	// zero records membership of the hash 0 itself, which the empty marker cannot represent. Using
	// MaxUint64 as the marker instead would only move the problem — and would cost a fill of the
	// whole table at construction and after every reset, where 0 lets make and clear do it.
	zero bool
}

// newShingleSet returns a set that can hold n members with a load factor of at most one half, which
// is what keeps linear probing's cluster lengths short enough that a lookup is one cache line in the
// overwhelming majority of cases.
func newShingleSet(n int) *shingleSet {
	// bits.Len64 rather than bits.Len: uint is 32 bits on the 32-bit targets this cross-builds for,
	// and a shift derived from a 32-bit width would index past the end of a 64-bit table.
	width := bits.Len64(uint64(2*n - 1))
	return &shingleSet{slots: make([]uint64, 1<<width), shift: uint(shingleHashBits - width)}
}

// add records h and reports whether it was NEW. A set that is full would loop forever here, which is
// why newShingleSet sizes for the caller's whole capacity and distinctBelow never exceeds it.
func (s *shingleSet) add(h uint64) bool {
	if h == 0 {
		if s.zero {
			return false
		}
		s.zero = true
		return true
	}
	i := (h * shingleSetMix) >> s.shift
	for s.slots[i] != 0 {
		if s.slots[i] == h {
			return false
		}
		i++
		if i == uint64(len(s.slots)) {
			i = 0
		}
	}
	s.slots[i] = h
	return true
}

// reset empties the set without reallocating it, so a trim costs one clear rather than a new table.
func (s *shingleSet) reset() {
	clear(s.slots)
	s.zero = false
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

package sketch

import (
	"encoding/binary"
	"fmt"
	"math"
	"math/rand"
	"strings"
	"testing"

	"github.com/qompack/qompack/internal/core"
	"github.com/stretchr/testify/require"
)

// These tests are package sketch, not package sketch_test, because TestMinHash_SubsamplingEngages
// asserts on minHashWithStats — the unexported form that also reports how many shingles survived
// the content-defined sampler. That count is the whole point of the cost bound: measuring it
// directly is a statement about the algorithm, where inferring it from a wall-clock reading would
// be a statement about the machine the suite happens to run on.
//
// Every document below is deterministic. Two generators are used and they are not
// interchangeable: mhDoc draws from math/rand for the statistical fixtures, where only
// reproducibility within a run matters, and mhFrozenInput uses an explicit LCG for
// TestMinHash_StableAcrossRuns, where the expected values are committed bytes and must not move
// because a standard-library generator changed.

const (
	// mhPerms is Appendix C's store.canonicalize.minhash.permutations.
	mhPerms = 128
	// mhShingleSize is DefaultShingleSize spelled out independently, so that a change to the
	// default shows up here as a failed expectation rather than as a silently different fixture.
	mhShingleSize = 8
	// mhNearDup is Appendix C's store.canonicalize.minhash.nearDupThreshold.
	mhNearDup = 0.9
	// mhDocBytes is the 4 KiB working-document size §8.1's near-duplicate case is written against.
	mhDocBytes = 4 << 10
	// mhBigDocBytes is 1 MiB: past MinHashSampleTarget by two orders of magnitude, so the sampler
	// is unambiguously engaged.
	mhBigDocBytes = 1 << 20
	// mhFakeLines is the line count of the fake `go test` transcript §8.1 item 1 describes.
	mhFakeLines = 200
)

// The two repeated build-log lines. A megabyte of either has over a million shingle POSITIONS and
// only as many DISTINCT shingles as the line is long, which is the divergence the sampler's retry
// exists for. They are ordinary tool output, not a contrived worst case.
const (
	// mhBuildLogLine is the line TestMinHash_RepetitiveDocumentIsNotEmpty repeats.
	mhBuildLogLine = "make[2]: Entering directory '/build/pkg/obj/x86_64-linux'\n"
	// mhBuildLogLine2 is a different line, repeated the same way, that must NOT come out as a
	// near-duplicate of the first.
	mhBuildLogLine2 = "warning: unused variable 'tmp' [-Wunused-variable] at src/a.c:41\n"
)

// The fixture seeds. They are named because several tests compare documents built from them, and a
// pair of tests that silently used the same seed for "a" and "b" would assert disjointness against
// two identical documents.
const (
	// mhSeedA and mhSeedB are the plan's 'a'-derived and 'b'-derived disjoint documents.
	mhSeedA = int64('a')
	mhSeedB = int64('b')
	// mhSeedShift is the plan's rand.NewSource(1) shift-invariance document.
	mhSeedShift = int64(1)
	// mhSeedBig seeds the 1 MiB subsampling documents.
	mhSeedBig = int64(1_000_003)
)

// mhCreated is the construction stamp every SigSketch case uses. It is a literal rather than a
// clock reading because a marshalled frame has to be byte-stable to be compared against itself.
const mhCreated = core.UnixMilli(1_700_000_000_000)

// mhOptions returns the Appendix C options: enabled, 128 permutations, the 8-byte default shingle,
// and the 0.9 near-duplicate threshold. NearDupThreshold is set even though this package never
// reads it, so that every case here travels the same fully populated struct canon (SP-04) will.
func mhOptions() MinHashOptions {
	return MinHashOptions{
		Enabled:          true,
		Permutations:     mhPerms,
		ShingleSize:      mhShingleSize,
		NearDupThreshold: mhNearDup,
	}
}

// mhDoc returns n pseudo-random bytes drawn from math/rand seeded with seed. gosec's G404 is
// excluded for _test.go files precisely for fixtures like this one: the bytes need to be
// reproducible and unstructured, not unpredictable.
func mhDoc(seed int64, n int) []byte {
	b := make([]byte, n)
	r := rand.New(rand.NewSource(seed))
	var word [8]byte
	for i := 0; i < n; i += len(word) {
		binary.LittleEndian.PutUint64(word[:], r.Uint64())
		copy(b[i:], word[:])
	}
	return b
}

// mhInsert returns data with ins spliced in at off, leaving both sides otherwise untouched. This is
// the edit MinHash has to survive: an insertion shifts every later byte's POSITION, so a sampler
// that selected shingles by index would resample the whole tail and report two nearly identical
// documents as unrelated.
func mhInsert(data, ins []byte, off int) []byte {
	out := make([]byte, 0, len(data)+len(ins))
	out = append(out, data[:off]...)
	out = append(out, ins...)
	return append(out, data[off:]...)
}

// mhShingles returns the set of distinct w-byte shingles of data — the set MinHash estimates over.
// It exists so that the loose bounds below ("≥ 0.9", "≤ 0.05") are stated against a fixture whose
// exact Jaccard has been measured rather than assumed.
func mhShingles(data []byte, w int) map[string]struct{} {
	out := make(map[string]struct{}, max(len(data)-w+1, 0))
	for i := 0; i+w <= len(data); i++ {
		out[string(data[i:i+w])] = struct{}{}
	}
	return out
}

// mhExactJaccard returns the true set Jaccard of a's and b's distinct w-shingles, computed the slow
// exact way. Every statistical assertion below logs it next to the estimate, so a failure says
// whether the estimator drifted or the fixture did.
func mhExactJaccard(a, b []byte, w int) float64 {
	sa, sb := mhShingles(a, w), mhShingles(b, w)
	inter := 0
	for k := range sa {
		if _, ok := sb[k]; ok {
			inter++
		}
	}
	union := len(sa) + len(sb) - inter
	if union == 0 {
		return 0
	}
	return float64(inter) / float64(union)
}

// mhRepeated returns exactly n bytes of line repeated end to end — a build log of thousands of
// identical lines, which is an ordinary tool result and the input the sampler's retry exists for.
// Its distinct shingle count is bounded by len(line) however long the document is, which is the
// whole of the divergence between shingle POSITIONS and distinct shingles.
func mhRepeated(line string, n int) []byte {
	out := make([]byte, 0, n+len(line))
	for len(out) < n {
		out = append(out, line...)
	}
	return out[:n]
}

// mhSelection reports where the sampler's halving retry must land for data: the rate of the first
// pass that keeps anything, the number of shingle POSITIONS kept at it, and the number of DISTINCT
// shingles in the document — the count the initial rate is NOT derived from, which is the whole of
// the bug this pins.
//
// It restates §5's selection rule (keep iff fnv1a64(shingle) ≤ MaxUint64/rate, halve on an empty
// pass) directly from the spec rather than calling minHashWithStats, so that it is an independent
// statement of where the retry should stop and not an echo of where it does. An implementation that
// jumped straight to rate 1 instead of halving would keep far more than this predicts and fail.
func mhSelection(data []byte, w int) (rate, kept, distinct int) {
	nsh := len(data) - w + 1
	hashes := make([]uint64, nsh)
	seen := make(map[uint64]struct{})
	for i := range hashes {
		hashes[i] = fnv1a64(data[i : i+w])
		seen[hashes[i]] = struct{}{}
	}
	for rate = (nsh + MinHashSampleTarget - 1) / MinHashSampleTarget; rate >= 1; rate /= 2 {
		keep := uint64(math.MaxUint64)
		if rate > 1 {
			keep = math.MaxUint64 / uint64(rate)
		}
		kept = 0
		for _, h := range hashes {
			if h <= keep {
				kept++
			}
		}
		if kept > 0 {
			break
		}
	}
	return rate, kept, len(seen)
}

// mhGoTestOutput returns a deterministic imitation of `go test -v` output: lines PASS lines whose
// only varying part is the case number, which is what a real transcript looks like and what §8.1
// item 1's "same test suite, one new failure" case is about.
func mhGoTestOutput(lines int) []byte {
	out := make([]string, 0, lines)
	for i := 0; i < lines; i++ {
		out = append(out, fmt.Sprintf("--- PASS: TestPackage/case_%03d (0.0%ds)", i, i%10))
	}
	return []byte(strings.Join(out, "\n") + "\n")
}

// TestMinHash_DisabledYieldsZero pins the off switch. canon (SP-04) builds MinHashOptions from
// config and calls MinHash unconditionally, so "disabled" has to be expressed in the return value
// rather than at the call site: the zero Signature, which Jaccard and IsNearDup then refuse to
// compare at all because Perms is 0.
//
// Mins must be nil and not an empty slice. store (SP-06) persists the Signature, and nil is what
// the compact form encodes as the two-byte "no signature" body.
func TestMinHash_DisabledYieldsZero(t *testing.T) {
	o := mhOptions()
	o.Enabled = false

	sig := MinHash(mhDoc(mhSeedA, mhDocBytes), o)
	require.Equal(t, Signature{}, sig)
	require.Zero(t, sig.Perms)
	require.Nil(t, sig.Mins, "the disabled signature carries no minima, not an empty slice")

	// It is inert on both sides of every comparison, at every threshold — including 0, which a
	// Perms-blind implementation would report as a near-duplicate of everything.
	live := MinHash(mhDoc(mhSeedA, mhDocBytes), mhOptions())
	require.Zero(t, sig.Jaccard(live))
	require.Zero(t, live.Jaccard(sig))
	require.False(t, sig.IsNearDup(live, 0))
	require.False(t, live.IsNearDup(sig, 0))
}

// TestMinHash_PermutationClamping pins the [MinPermutations, MaxPermutations] bound. It is the same
// [16, 512] range 00-ARCHITECTURE.md §11.3 makes config.Validate enforce, applied again here so
// that a hand-edited .qompack/config.json cannot make the daemon allocate an absurd signature —
// and so that a Signature can never be produced that MaxPermutations would later refuse to decode.
//
// The clamp is clampInt, not clamp: Permutations is an int, and routing it through float64 would
// reintroduce exactly the NaN and precision hazards util.go documents.
func TestMinHash_PermutationClamping(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   int
		want uint16
	}{
		{"4 clamps up to MinPermutations", 4, MinPermutations},
		{"128 is Appendix C and is untouched", mhPerms, mhPerms},
		{"9999 clamps down to MaxPermutations", 9_999, MaxPermutations},
		{"0 clamps up to MinPermutations", 0, MinPermutations},
		{"negative clamps up to MinPermutations", -7, MinPermutations},
		{"the exact minimum is untouched", MinPermutations, MinPermutations},
		{"the exact maximum is untouched", MaxPermutations, MaxPermutations},
		{"one past the maximum clamps down", MaxPermutations + 1, MaxPermutations},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := mhOptions()
			o.Permutations = tc.in

			sig := MinHash(mhDoc(mhSeedA, mhDocBytes), o)
			require.Equal(t, tc.want, sig.Perms)
			require.Len(t, sig.Mins, int(tc.want),
				"Perms and len(Mins) must agree, or Jaccard refuses the signature")

			// A clamped signature is still a working one: §12.3, nothing in this package fails by
			// panicking or by handing back an unusable value.
			require.Equal(t, 1.0, sig.Jaccard(sig))
		})
	}
}

// TestMinHash_ShingleSizeClamping pins the other clamp, which has no direct accessor and so is
// observed through the one thing the shingle width decides: the shortest document that is not
// empty. A document of exactly w bytes yields one shingle; w−1 bytes yields none and must return
// the canonical empty signature.
//
// Both ends matter. A ShingleSize of 1 would make every single byte a shingle, so any two documents
// drawn from the same alphabet would look nearly identical; a ShingleSize of 4096 would make every
// document under 4 KiB empty, and §8.1's near-duplicate detection would silently stop working.
func TestMinHash_ShingleSizeClamping(t *testing.T) {
	for _, tc := range []struct {
		name  string
		in    int
		wantW int
	}{
		{"0 falls back to DefaultShingleSize", 0, DefaultShingleSize},
		{"negative falls back to DefaultShingleSize", -3, DefaultShingleSize},
		{"1 clamps up to the minimum", 1, 2},
		{"the default is untouched", DefaultShingleSize, DefaultShingleSize},
		{"1000 clamps down to the maximum", 1_000, 64},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := mhOptions()
			o.ShingleSize = tc.in
			doc := mhDoc(mhSeedA, tc.wantW+mhShingleSize)

			atW := MinHash(doc[:tc.wantW], o)
			require.Len(t, atW.Mins, mhPerms)
			require.NotEqual(t, uint64(math.MaxUint64), atW.Mins[0],
				"a document of exactly the effective shingle width yields one shingle")

			below := MinHash(doc[:tc.wantW-1], o)
			require.Len(t, below.Mins, mhPerms)
			for i, m := range below.Mins {
				require.Equal(t, uint64(math.MaxUint64), m,
					"one byte short of the effective width there are no shingles at all (position %d)", i)
			}
		})
	}
}

// TestMinHash_ShortInputIsEmptySignature pins the canonical empty-document signature: P copies of
// MaxUint64, not a nil slice and not Perms == 0. The distinction is load-bearing. A zero Signature
// means "MinHash was disabled"; an all-MaxUint64 signature means "MinHash ran and found no
// shingles", and the two must not be confused by a consumer deciding whether it holds a comparable
// value.
//
// Two empty documents therefore compare as Jaccard == 1, which is the correct answer: their shingle
// sets are both empty and identical.
func TestMinHash_ShortInputIsEmptySignature(t *testing.T) {
	sig := MinHash([]byte("abc"), mhOptions())
	require.Equal(t, uint16(mhPerms), sig.Perms)
	require.Len(t, sig.Mins, mhPerms)
	for i, m := range sig.Mins {
		require.Equal(t, uint64(math.MaxUint64), m, "position %d of an empty-document signature", i)
	}

	other := MinHash([]byte("xy"), mhOptions())
	require.Equal(t, 1.0, sig.Jaccard(other),
		"two documents with no shingles have identical (empty) shingle sets")
	require.True(t, sig.IsNearDup(other, mhNearDup))
}

// TestMinHash_IdenticalInputs is the sanity floor: the same bytes must produce the same signature,
// exactly, and compare as a perfect match. If this fails nothing else in the file means anything,
// because every other case measures a DIFFERENCE from this baseline.
func TestMinHash_IdenticalInputs(t *testing.T) {
	doc := mhDoc(mhSeedA, mhDocBytes)
	a := MinHash(doc, mhOptions())
	b := MinHash(append([]byte(nil), doc...), mhOptions())

	require.Equal(t, a, b, "MinHash is a pure function of the bytes it is given")
	require.Equal(t, 1.0, a.Jaccard(b))
	require.True(t, a.IsNearDup(b, mhNearDup))
}

// TestMinHash_DisjointInputs is the opposite floor: unrelated content must not look related. The
// two documents are checked to share literally no 8-gram before the estimate is asserted, so the
// 0.05 bound is measured against a fixture whose exact Jaccard is known to be 0 — an estimator that
// reported similarity here would be finding structure in the permutation coefficients rather than
// in the data.
func TestMinHash_DisjointInputs(t *testing.T) {
	a := mhDoc(mhSeedA, mhDocBytes)
	b := mhDoc(mhSeedB, mhDocBytes)

	exact := mhExactJaccard(a, b, mhShingleSize)
	require.Zero(t, exact, "fixture sanity: the two documents must share no %d-gram", mhShingleSize)

	got := MinHash(a, mhOptions()).Jaccard(MinHash(b, mhOptions()))
	t.Logf("disjoint 4 KiB documents: exact Jaccard %.4f, MinHash estimate %.4f", exact, got)
	require.LessOrEqual(t, got, 0.05)
}

// TestMinHash_OneNewFailure is §8.1 item 1 itself — "same test suite, one new failure" — which is
// the case this whole file exists to serve. store (SP-06) uses the answer to decide whether to
// keep a delta against the previous transcript instead of the full text, so a false negative here
// costs a full copy of a nearly identical document in the append-only index.
func TestMinHash_OneNewFailure(t *testing.T) {
	before := mhGoTestOutput(mhFakeLines)
	after := append(append([]byte(nil), before...),
		"--- FAIL: TestPackage/case_200 (0.01s)\n"...)

	sigBefore, sigAfter := MinHash(before, mhOptions()), MinHash(after, mhOptions())
	exact := mhExactJaccard(before, after, mhShingleSize)
	got := sigBefore.Jaccard(sigAfter)
	t.Logf("%d-line transcript (%d bytes, %d distinct shingles) plus one FAIL line: exact %.4f, estimate %.4f",
		mhFakeLines, len(before), len(mhShingles(before, mhShingleSize)), exact, got)

	require.GreaterOrEqual(t, got, mhNearDup)
	require.True(t, sigBefore.IsNearDup(sigAfter, mhNearDup))
}

// TestMinHash_ShiftInvariance is the case that decides the sampler's design. Inserting 40 bytes at
// offset 100 destroys the 7 shingles that straddle the insertion point and adds 47 new ones out of
// roughly 4 089 — an exact Jaccard near 0.987 — but it also moves the POSITION of every subsequent
// byte. A positional sampler (every rate-th shingle) would select an entirely different subset
// after the shift and report two near-identical documents as unrelated; content-defined selection
// keeps the same shingles because the decision is a function of the shingle's bytes alone.
//
// The document is seeded rand.NewSource(1) so a failure is reproducible rather than a coin flip.
func TestMinHash_ShiftInvariance(t *testing.T) {
	d := mhDoc(mhSeedShift, mhDocBytes)
	ins := mhDoc(mhSeedB, 40)
	dPrime := mhInsert(d, ins, 100)
	require.Len(t, dPrime, mhDocBytes+len(ins))

	exact := mhExactJaccard(d, dPrime, mhShingleSize)
	got := MinHash(d, mhOptions()).Jaccard(MinHash(dPrime, mhOptions()))
	t.Logf("4 KiB document with %d bytes inserted at offset 100: exact Jaccard %.4f, estimate %.4f",
		len(ins), exact, got)

	require.GreaterOrEqual(t, exact, mhNearDup, "fixture sanity: the edit must be a small one")
	require.GreaterOrEqual(t, got, mhNearDup)
}

// TestMinHash_SubsamplingEngages pins the cost bound directly. Without subsampling a 1 MiB document
// costs 1 048 569 shingles × 128 permutations ≈ 134 million multiply-compares, which is far outside
// §8.1's hook budget; with it the work is bounded at roughly MinHashSampleTarget shingles whatever
// the input size, so the cost of a signature is capped rather than linear in the document.
//
// The count is read from minHashWithStats rather than inferred from a timing measurement, because a
// timing assertion measures the machine and this one measures the algorithm.
func TestMinHash_SubsamplingEngages(t *testing.T) {
	sig, kept := minHashWithStats(mhDoc(mhSeedBig, mhBigDocBytes), mhOptions())
	shingles := mhBigDocBytes - mhShingleSize + 1

	require.Equal(t, uint16(mhPerms), sig.Perms)
	require.Len(t, sig.Mins, mhPerms)
	t.Logf("1 MiB input: %d shingles, %d kept (1 in %.1f), target %d",
		shingles, kept, float64(shingles)/float64(kept), MinHashSampleTarget)

	require.LessOrEqual(t, kept, 2*MinHashSampleTarget, "the sampler must bound the work")
	// The upper bound above is a property of the ALGORITHM; this lower bound is a property of THIS
	// fixture, and the distinction matters. A pseudo-random megabyte has essentially no repeated
	// 8-grams, so its distinct-shingle count is its position count and the rate lands where the
	// arithmetic intends. A repetitive document keeps far fewer than half the target and is right to
	// — TestMinHash_RepetitiveDocumentIsNotEmpty owns that case, and asserting the same floor there
	// would be asserting something false.
	require.GreaterOrEqual(t, kept, MinHashSampleTarget/2,
		"a document whose shingles are all distinct must land near the target")

	t.Run("below the target every shingle is kept", func(t *testing.T) {
		// rate <= 1 is the other branch: a document under MinHashSampleTarget shingles is measured
		// in full, so a small document loses no accuracy to a sampler it never needed.
		small := mhDoc(mhSeedA, mhDocBytes)
		_, keptSmall := minHashWithStats(small, mhOptions())
		require.Equal(t, len(small)-mhShingleSize+1, keptSmall)
	})

	t.Run("an empty document keeps nothing", func(t *testing.T) {
		_, keptEmpty := minHashWithStats([]byte("abc"), mhOptions())
		require.Zero(t, keptEmpty)
	})
}

// TestMinHash_RepetitiveDocumentIsNotEmpty is the case the sampler's halving retry exists for, and
// it is a correction to the plan's algorithm rather than an elaboration of it.
//
// The rate is derived from nsh — shingle POSITIONS — but selection applies to distinct shingle
// HASHES. A megabyte of one repeated line has over a million positions and therefore rate 128, but
// only as many distinct shingles as the line is long, each surviving with probability 1/128. The
// odds that none survives are better than even. When that happens every minimum stays MaxUint64,
// which is BYTE-IDENTICAL to the canonical empty-document signature of step 4 — so the document
// would report Jaccard 1 against an empty document, against "abc", and against any other document
// that also sampled to nothing, and SP-06 would store a delta in place of the text.
//
// The fixture is chosen so that the naive single-pass rate really does keep nothing: that is
// asserted first, so this test fails loudly if it ever stops exercising the retry.
func TestMinHash_RepetitiveDocumentIsNotEmpty(t *testing.T) {
	doc := mhRepeated(mhBuildLogLine, mhBigDocBytes)
	nsh := len(doc) - mhShingleSize + 1
	initialRate := (nsh + MinHashSampleTarget - 1) / MinHashSampleTarget

	rate, wantKept, distinct := mhSelection(doc, mhShingleSize)
	t.Logf("%d bytes of one %d-byte line: %d shingle positions but only %d distinct; "+
		"initial rate %d keeps nothing, the retry lands on rate %d keeping %d positions",
		len(doc), len(mhBuildLogLine), nsh, distinct, initialRate, rate, wantKept)
	require.Less(t, rate, initialRate,
		"fixture sanity: this document must be one the single-pass sampler throws away entirely")

	sig, kept := minHashWithStats(doc, mhOptions())
	require.Equal(t, wantKept, kept, "the retry must halve, not jump straight to rate 1")
	require.Positive(t, kept)

	// The signature must not BE the empty-document signature.
	empty := MinHash(nil, mhOptions())
	require.Len(t, sig.Mins, mhPerms)
	require.NotEqual(t, empty.Mins, sig.Mins)
	for i, m := range sig.Mins {
		require.NotEqual(t, uint64(math.MaxUint64), m,
			"every permutation saw at least one kept shingle (position %d)", i)
	}

	// And must not COMPARE as one, against any of the three ways a document can be empty.
	for _, tc := range []struct {
		name string
		o    Signature
	}{
		{"a nil document", empty},
		{"a document shorter than one shingle", MinHash([]byte("abc"), mhOptions())},
		{"a different repeated line", MinHash(mhRepeated(mhBuildLogLine2, mhBigDocBytes), mhOptions())},
	} {
		t.Run("not a near-duplicate of "+tc.name, func(t *testing.T) {
			require.Zero(t, sig.Jaccard(tc.o))
			require.False(t, sig.IsNearDup(tc.o, mhNearDup))
			require.False(t, tc.o.IsNearDup(sig, mhNearDup))
		})
	}

	t.Run("but is a near-duplicate of itself and of a longer run of the same line", func(t *testing.T) {
		// The positive half: content-defined selection is what makes two build logs of the same
		// repeated line agree regardless of how many times it repeats.
		longer := MinHash(mhRepeated(mhBuildLogLine, mhBigDocBytes+len(mhBuildLogLine)*8), mhOptions())
		require.Equal(t, 1.0, sig.Jaccard(sig))
		require.True(t, sig.IsNearDup(longer, mhNearDup))
	})
}

// TestMinHash_SubsamplingStillShiftInvariant is the previous two cases combined, and it is the one
// a positional sampler cannot fake. At 1 MiB the sampler is engaged, so only about one shingle in
// 128 is looked at; inserting bytes in the middle shifts every later byte. The estimate survives
// only because selection is content-defined: the same shingles are chosen on both sides of the
// edit, minus the small fraction the slightly different sampling rate excludes.
//
// The insertion is 4 093 bytes — prime, and deliberately NOT the round 4 096 — so that the test
// keeps its discriminating power for the right reason. A positional "keep every rate-th index"
// sampler shifted by an exact multiple of the rate realigns in the tail and would score ≈ 1.0 here;
// shifted by a length coprime to the rate it cannot realign at any rate the two documents land on.
// The implementation is unambiguously content-defined either way, so this is about what the test
// would catch, not about what the code does.
func TestMinHash_SubsamplingStillShiftInvariant(t *testing.T) {
	const insertBytes = 4_093
	d := mhDoc(mhSeedBig, mhBigDocBytes)
	dPrime := mhInsert(d, mhDoc(mhSeedB, insertBytes), mhBigDocBytes/2)

	a, keptA := minHashWithStats(d, mhOptions())
	b, keptB := minHashWithStats(dPrime, mhOptions())
	got := a.Jaccard(b)
	t.Logf("1 MiB vs 1 MiB + %d bytes inserted mid-way: %d and %d shingles kept, estimate %.4f",
		insertBytes, keptA, keptB, got)

	require.GreaterOrEqual(t, got, 0.85)
}

// TestMinHash_Symmetric pins Jaccard's symmetry, including on the paths that return 0. Symmetry is
// not free: the guard has to check BOTH signatures' Mins against BOTH declared widths, because the
// comparison loop ranges over the receiver and would index past the end of a short argument. An
// implementation that checked only the receiver would be asymmetric in exactly the case where it
// also panics.
func TestMinHash_Symmetric(t *testing.T) {
	for _, tc := range []struct {
		name string
		a, b Signature
	}{
		{"identical", MinHash(mhDoc(mhSeedA, mhDocBytes), mhOptions()), MinHash(mhDoc(mhSeedA, mhDocBytes), mhOptions())},
		{"overlapping", MinHash(mhDoc(mhSeedA, mhDocBytes), mhOptions()), MinHash(mhInsert(mhDoc(mhSeedA, mhDocBytes), mhDoc(mhSeedB, 64), 512), mhOptions())},
		{"disjoint", MinHash(mhDoc(mhSeedA, mhDocBytes), mhOptions()), MinHash(mhDoc(mhSeedB, mhDocBytes), mhOptions())},
		{"zero against real", Signature{}, MinHash(mhDoc(mhSeedA, mhDocBytes), mhOptions())},
		{"two zeroes", Signature{}, Signature{}},
		{"mismatched widths", MinHash(mhDoc(mhSeedA, mhDocBytes), mhOptions()), Signature{Perms: 4, Mins: make([]uint64, 4)}},
		{"a lying width", Signature{Perms: mhPerms, Mins: make([]uint64, mhPerms)}, Signature{Perms: mhPerms, Mins: make([]uint64, 3)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.a.Jaccard(tc.b), tc.b.Jaccard(tc.a))
			require.Equal(t, tc.a.IsNearDup(tc.b, mhNearDup), tc.b.IsNearDup(tc.a, mhNearDup))
		})
	}
}

// TestMinHash_IncomparableWidths pins the refusal to compare signatures of different widths. The
// two are not merely hard to compare — they are computed from different permutation sets, so
// position i of a 128-wide signature and position i of a 256-wide one are minima of the same
// function and would agree far too often. Reporting 0 is the honest answer; a partial comparison
// over the shared prefix would be a fabricated one.
func TestMinHash_IncomparableWidths(t *testing.T) {
	doc := mhDoc(mhSeedA, mhDocBytes)

	narrow := MinHash(doc, mhOptions())
	wide := MinHash(doc, MinHashOptions{Enabled: true, Permutations: 256, ShingleSize: mhShingleSize})
	require.Equal(t, uint16(mhPerms), narrow.Perms)
	require.Equal(t, uint16(256), wide.Perms)

	require.Zero(t, narrow.Jaccard(wide))
	require.Zero(t, wide.Jaccard(narrow))
	require.False(t, narrow.IsNearDup(wide, mhNearDup))
	require.False(t, wide.IsNearDup(narrow, mhNearDup))
	require.False(t, narrow.IsNearDup(wide, 0), "not even at a zero threshold")
}

// TestMinHash_ZeroSignatureComparesZero pins the Perms != 0 guard in IsNearDup, which is the one
// guard a Jaccard-only implementation would miss: Jaccard already reports 0 for the zero signature,
// and 0 >= 0.0 is true, so a threshold of 0 would make the zero signature a near-duplicate of
// everything — including of itself. SP-06 calls IsNearDup with a configured threshold that a
// hand-edited config can set to 0, so this is reachable, not theoretical.
func TestMinHash_ZeroSignatureComparesZero(t *testing.T) {
	zero := Signature{}
	live := MinHash(mhDoc(mhSeedA, mhDocBytes), mhOptions())

	require.Zero(t, zero.Jaccard(live))
	require.Zero(t, live.Jaccard(zero))
	require.Zero(t, zero.Jaccard(zero))

	require.False(t, zero.IsNearDup(live, 0))
	require.False(t, live.IsNearDup(zero, 0))
	require.False(t, zero.IsNearDup(zero, 0), "the zero signature is not a near-duplicate of itself")
}

// TestSignature_CompactRoundTrip pins the compact wire form: a uint16 width and then that many
// little-endian uint64s, with no QPKS header because the store record line that carries it already
// frames it. The re-marshal must be byte-identical, since store (SP-06) writes these bytes into an
// append-only index where a representation that drifted would make yesterday's record and today's
// incomparable.
func TestSignature_CompactRoundTrip(t *testing.T) {
	sig := MinHash(mhDoc(mhSeedA, mhDocBytes), mhOptions())

	b, err := sig.MarshalBinary()
	require.NoError(t, err)
	require.Len(t, b, 2+8*mhPerms)
	require.Equal(t, 1_026, len(b), "128 permutations is a 1 026-byte record field")
	require.Equal(t, uint16(mhPerms), binary.LittleEndian.Uint16(b[:2]))

	var got Signature
	require.NoError(t, got.UnmarshalBinary(b))
	require.Equal(t, sig, got)

	again, err := got.MarshalBinary()
	require.NoError(t, err)
	require.Equal(t, b, again, "one logical signature has exactly one representation")

	t.Run("the zero signature round-trips to two bytes", func(t *testing.T) {
		zb, err := Signature{}.MarshalBinary()
		require.NoError(t, err)
		require.Equal(t, []byte{0, 0}, zb)

		var back Signature
		require.NoError(t, back.UnmarshalBinary(zb))
		require.Equal(t, Signature{}, back)
		require.Nil(t, back.Mins, "Perms == 0 decodes to nil Mins, not an empty slice")
	})

	t.Run("the empty-document signature round-trips", func(t *testing.T) {
		empty := MinHash(nil, mhOptions())
		eb, err := empty.MarshalBinary()
		require.NoError(t, err)

		var back Signature
		require.NoError(t, back.UnmarshalBinary(eb))
		require.Equal(t, empty, back)
	})
}

// TestSignature_UnmarshalRejects is the decoder's threat model: a forged record field. Perms is
// both a declared length and an allocation size, so it is validated against MaxPermutations BEFORE
// anything is allocated — and the oversize case is held to zero allocations, which is what makes
// the check a bound rather than a comment.
//
// A zero-length input is ErrTruncated and never a silently empty signature: a record whose
// signature field was lost must not decode into "MinHash was disabled", because that reads as a
// deliberate choice rather than as damage.
//
// Every case decodes into a POPULATED receiver — a real 128-permutation signature — and asserts it
// is unchanged afterwards. Decoding into a fresh zero value would make "the receiver is untouched"
// a comparison of the zero value against the zero value, which holds however the decoder behaves;
// the property under test is that a failed decode does not half-overwrite a signature the caller
// still holds.
func TestSignature_UnmarshalRejects(t *testing.T) {
	full := func(perms uint16, mins int) []byte {
		b := make([]byte, 2+8*mins)
		binary.LittleEndian.PutUint16(b[:2], perms)
		return b
	}
	// A deep copy, not a struct copy: a struct copy would share the Mins array, and an
	// implementation that wrote into the existing minima in place would compare equal to it.
	populated := func() (sig, before Signature) {
		sig = MinHash(mhDoc(mhSeedA, mhDocBytes), mhOptions())
		return sig, Signature{Perms: sig.Perms, Mins: append([]uint64(nil), sig.Mins...)}
	}

	for _, tc := range []struct {
		name string
		in   []byte
		want error
	}{
		{"nil", nil, ErrTruncated},
		{"empty", []byte{}, ErrTruncated},
		{"one byte", []byte{0x80}, ErrTruncated},
		{"128 declared, no body at all", full(mhPerms, 0), ErrTruncated},
		{"128 declared, 100 bytes of mins", append(full(mhPerms, 0), make([]byte, 100)...), ErrTruncated},
		{"128 declared, long body", full(mhPerms, mhPerms+1), ErrTruncated},
		{"Perms == 0 with a trailing byte", []byte{0, 0, 0}, ErrTruncated},
		{"MaxPermutations+1, header only", full(MaxPermutations+1, 0), ErrMalformed},
		{"MaxPermutations+1, full body", full(MaxPermutations+1, MaxPermutations+1), ErrMalformed},
		{"the largest possible declared width", full(math.MaxUint16, 0), ErrMalformed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sig, before := populated()
			err := sig.UnmarshalBinary(tc.in)
			require.ErrorIs(t, err, tc.want)
			require.Equal(t, before, sig, "a rejected frame must leave the receiver untouched")
		})
	}

	t.Run("the oversize case allocates nothing", func(t *testing.T) {
		sig, _ := populated()
		b := full(MaxPermutations+1, MaxPermutations+1)
		require.Zero(t, testing.AllocsPerRun(100, func() {
			_ = sig.UnmarshalBinary(b)
		}), "Perms is checked against MaxPermutations before the make()")
	})

	t.Run("a nil receiver reports rather than panicking", func(t *testing.T) {
		// A Signature decoded straight into a *Signature field that was never allocated. §12.3:
		// every entry point in this package fails by reporting, because a hook that dies takes
		// observability down with it.
		var sig *Signature
		require.ErrorIs(t, sig.UnmarshalBinary(full(0, 0)), ErrMalformed)
	})
}

// TestSignature_MarshalRejectsInconsistent pins the encoder's half of the same invariant. A
// Signature whose Perms and Mins disagree, or whose Perms is past MaxPermutations, is one no
// MinHash call can produce — but it IS constructible by hand, and writing it would put bytes in the
// append-only index that this package's own decoder refuses. Refusing to write is the cheaper
// failure: errors.go's ceiling rule states it for the framed sketches, and this is the same rule
// for the compact form.
func TestSignature_MarshalRejectsInconsistent(t *testing.T) {
	for _, tc := range []struct {
		name string
		sig  Signature
	}{
		{"more minima than declared", Signature{Perms: 2, Mins: make([]uint64, 3)}},
		{"fewer minima than declared", Signature{Perms: 4, Mins: make([]uint64, 1)}},
		{"declared width with no minima", Signature{Perms: mhPerms}},
		{"minima with no declared width", Signature{Mins: make([]uint64, 3)}},
		{"past MaxPermutations", Signature{Perms: MaxPermutations + 1, Mins: make([]uint64, MaxPermutations+1)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, err := tc.sig.MarshalBinary()
			require.ErrorIs(t, err, ErrMalformed)
			require.Nil(t, b, "nothing is written when nothing can be read back")
		})
	}
}

// TestSignature_JaccardRejectsLyingWidth pins the length guard Jaccard needs on both operands. The
// comparison loop ranges over the receiver's Mins and indexes the argument's, so a signature whose
// declared Perms exceeds its Mins would panic an implementation that trusted Perms — and MinHash
// itself never produces one, so the only way such a value reaches Jaccard is from a decoded record
// or from a caller building the struct directly, which is exactly when a panic is least affordable.
//
// The threshold here is the configured 0.9 and not 0, deliberately. IsNearDup's zero-threshold
// refusal is a statement about the ZERO signature (Perms == 0) and nothing else: a caller that asks
// "is anything with my width a near-duplicate at threshold 0" is answered yes, because that is what
// a zero threshold means. TestMinHash_ZeroSignatureComparesZero owns the zero-threshold case.
func TestSignature_JaccardRejectsLyingWidth(t *testing.T) {
	live := MinHash(mhDoc(mhSeedA, mhDocBytes), mhOptions())

	for _, tc := range []struct {
		name string
		bad  Signature
	}{
		{"declared wider than its minima", Signature{Perms: mhPerms, Mins: make([]uint64, 4)}},
		{"declared narrower than its minima", Signature{Perms: mhPerms, Mins: make([]uint64, mhPerms+1)}},
		{"declared width with nil minima", Signature{Perms: mhPerms}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.NotPanics(t, func() {
				require.Zero(t, live.Jaccard(tc.bad))
				require.Zero(t, tc.bad.Jaccard(live))
				require.Zero(t, tc.bad.Jaccard(tc.bad))
			})
			require.False(t, live.IsNearDup(tc.bad, mhNearDup))
			require.False(t, tc.bad.IsNearDup(live, mhNearDup))
			require.False(t, tc.bad.IsNearDup(tc.bad, mhNearDup))
		})
	}
}

// TestSigSketch_RoundTrip pins the framed form. SigSketch exists so KindMinHash is reachable
// through the same CRC-checked container as the other four sketches — the compact form has no
// checksum of its own, because the store record line that carries it is checksummed instead, so a
// signature written as a standalone FILE needs the frame to be able to detect bit rot at all.
func TestSigSketch_RoundTrip(t *testing.T) {
	s := &SigSketch{Sig: MinHash(mhDoc(mhSeedA, mhDocBytes), mhOptions()), Created: mhCreated}

	// The whole point of the type, asserted at compile time: KindMinHash has to be reachable
	// through the same interface Save and Load take.
	var asSketch Sketch = s
	require.Equal(t, KindMinHash, asSketch.Header().Kind)

	frame, err := s.MarshalBinary()
	require.NoError(t, err)

	hdr, body, err := DecodeHeader(frame)
	require.NoError(t, err)
	require.Equal(t, KindMinHash, hdr.Kind)
	require.Equal(t, FormatVersion, hdr.Ver)
	require.Equal(t, mhCreated, hdr.Created)
	require.Equal(t, uint64(mhPerms), hdr.Count, "Count is the signature width")
	require.Equal(t, float64(mhPerms), hdr.Params[paramPerms])
	require.Len(t, body, 2+8*mhPerms, "the body is the compact form verbatim")

	var got SigSketch
	require.NoError(t, got.UnmarshalBinary(frame))
	require.Equal(t, s.Sig, got.Sig)
	require.Equal(t, mhCreated, got.Created)
	require.Equal(t, 1.0, s.Sig.Jaccard(got.Sig))

	again, err := got.MarshalBinary()
	require.NoError(t, err)
	require.Equal(t, frame, again, "a given logical state has exactly one representation")

	require.Equal(t, s.Header(), got.Header(), "the round trip reproduces an equal Header")
}

// TestSigSketch_UnmarshalRejects walks the framed decoder's refusals. DecodeHeader has already
// checked the magic, the version, the CRC and every declared length by the time decodeV1 runs, so
// what is left to this type is the two questions only it can answer: is this frame a MinHash at
// all, and does its header agree with its body?
//
// Every case decodes into a POPULATED receiver — a sketch already holding another document's
// signature and its own Created stamp — and asserts both survive unchanged. decodeV1 assigns only
// after every check has passed, but a fresh zero receiver could not tell that apart from a decoder
// that assigned as it went and then failed.
func TestSigSketch_UnmarshalRejects(t *testing.T) {
	body, err := MinHash(mhDoc(mhSeedA, mhDocBytes), mhOptions()).MarshalBinary()
	require.NoError(t, err)

	perms := func(v float64) map[string]float64 { return map[string]float64{paramPerms: v} }
	frame := func(ver uint16, kind Kind, params map[string]float64, b []byte) []byte {
		f, err := EncodeHeader(Header{Ver: ver, Kind: kind, Params: params}, b)
		require.NoError(t, err)
		return f
	}
	// A deep copy of Mins, not a struct copy, so that a decoder writing into the existing minima in
	// place could not compare equal to the "before" value.
	populated := func() (s, before SigSketch) {
		sig := MinHash(mhDoc(mhSeedB, mhDocBytes), mhOptions())
		return SigSketch{Sig: sig, Created: mhCreated},
			SigSketch{
				Sig:     Signature{Perms: sig.Perms, Mins: append([]uint64(nil), sig.Mins...)},
				Created: mhCreated,
			}
	}

	for _, tc := range []struct {
		name  string
		frame []byte
		want  error
	}{
		{"a frame of another kind", frame(FormatVersion, KindBloom, perms(mhPerms), body), ErrKindMismatch},
		// EncodeHeader writes Ver verbatim, which is the only way to build a newer-build frame.
		// DecodeHeader refuses it before decodeV1's version switch is reached, so that switch's
		// default arm is unreachable by construction — this row pins the refusal, not the arm.
		{"a frame from a newer build", frame(FormatVersion+1, KindMinHash, perms(mhPerms), body), ErrUnsupportedVersion},
		{"no perms param", frame(FormatVersion, KindMinHash, nil, body), ErrMalformed},
		{"a perms param past MaxPermutations", frame(FormatVersion, KindMinHash, perms(MaxPermutations+1), body), ErrMalformed},
		{"a header that disagrees with its body", frame(FormatVersion, KindMinHash, perms(mhPerms/2), body), ErrMalformed},
		{"a truncated body", frame(FormatVersion, KindMinHash, perms(mhPerms), body[:len(body)-8]), ErrTruncated},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, before := populated()
			require.ErrorIs(t, s.UnmarshalBinary(tc.frame), tc.want)
			require.Equal(t, before, s, "a rejected frame must leave the receiver untouched")
		})
	}

	t.Run("a nil receiver reports rather than panicking", func(t *testing.T) {
		var s *SigSketch
		_, err := s.MarshalBinary()
		require.ErrorIs(t, err, ErrMalformed)
		require.ErrorIs(t,
			s.UnmarshalBinary(frame(FormatVersion, KindMinHash, perms(mhPerms), body)), ErrMalformed)
	})

	t.Run("an inconsistent signature is not written", func(t *testing.T) {
		s := &SigSketch{Sig: Signature{Perms: mhPerms, Mins: make([]uint64, 2)}}
		_, err := s.MarshalBinary()
		require.ErrorIs(t, err, ErrMalformed)
	})
}

// The frozen fixture. mhFrozenInput uses an explicit 64-bit LCG (the MMIX constants) rather than
// math/rand so that the four constants below depend on nothing outside this file: they are a
// format assertion, and they must not move because a standard-library generator changed.
const (
	// mhFrozenBytes is the fixture length: 1 KiB, comfortably under MinHashSampleTarget so that
	// every shingle is measured and the sampler is not part of what is being frozen.
	mhFrozenBytes = 1 << 10
	// mhFrozenSeed seeds the LCG.
	mhFrozenSeed = uint64(1)
	// mhLCGMul and mhLCGInc are Knuth's MMIX linear-congruential constants.
	mhLCGMul = uint64(6364136223846793005)
	mhLCGInc = uint64(1442695040888963407)
)

// The first four minima of MinHash(mhFrozenInput(), {128, 8}), recorded from the first green run
// and re-confirmed on the next. They are a FROZEN-FORMAT assertion, not a golden convenience:
// signatures are persisted in an append-only index (store.ToolUseRecord.Signature), so a change to
// fnv1a64, to splitmix64, to the a[i] = splitmix64(2i)|1 / b[i] = splitmix64(2i+1) coefficient
// derivation, or to the v = a·h + b permutation makes every stored signature incomparable with
// every new one — silently, since both sides still look like well-formed signatures. If this test
// fails, the question is not "what are the new values" but "what changed, and what happens to the
// signatures already on disk".
const (
	mhFrozenMin0 = uint64(11_318_863_401_809_556)
	mhFrozenMin1 = uint64(22_627_498_123_505_695)
	mhFrozenMin2 = uint64(2_170_177_795_160_030)
	mhFrozenMin3 = uint64(1_049_968_054_617_626)
)

// mhFrozenInput builds the 1 KiB fixture TestMinHash_StableAcrossRuns is frozen against.
func mhFrozenInput() []byte {
	b := make([]byte, mhFrozenBytes)
	x := mhFrozenSeed
	for i := range b {
		x = x*mhLCGMul + mhLCGInc
		b[i] = byte(x >> 56)
	}
	return b
}

// TestMinHash_StableAcrossRuns is the frozen-format assertion described above.
func TestMinHash_StableAcrossRuns(t *testing.T) {
	in := mhFrozenInput()
	require.Len(t, in, mhFrozenBytes)

	sig := MinHash(in, MinHashOptions{Enabled: true, Permutations: mhPerms, ShingleSize: mhShingleSize})
	require.Equal(t, uint16(mhPerms), sig.Perms)
	require.Len(t, sig.Mins, mhPerms)

	t.Logf("frozen 1 KiB fixture: Mins[0..3] = %d, %d, %d, %d",
		sig.Mins[0], sig.Mins[1], sig.Mins[2], sig.Mins[3])
	require.Equal(t, mhFrozenMin0, sig.Mins[0])
	require.Equal(t, mhFrozenMin1, sig.Mins[1])
	require.Equal(t, mhFrozenMin2, sig.Mins[2])
	require.Equal(t, mhFrozenMin3, sig.Mins[3])

	// Stable within the run as well as across them: MinHash holds no state between calls.
	require.Equal(t, sig, MinHash(mhFrozenInput(),
		MinHashOptions{Enabled: true, Permutations: mhPerms, ShingleSize: mhShingleSize}))
}

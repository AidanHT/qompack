package chunk_test

import (
	"bytes"
	"fmt"
	"sort"
	"testing"

	"github.com/qompack/qompack/internal/chunk"
	"github.com/qompack/qompack/internal/core"
	"github.com/stretchr/testify/require"
)

// Boundary stability is the entire reason this package exists (00-ARCHITECTURE.md §5.5). Fixed-size
// blocking would be simpler, faster and smaller; it is rejected because inserting one byte at the
// front of a file shifts every subsequent block and destroys all deduplication against the file's
// own previous version. Content-defined boundaries are supposed to re-synchronize a bounded
// distance after any local edit, so an edited object shares almost all of its chunks with the
// version before it.
//
// "Almost all" has to be measured, not asserted qualitatively, because the failure is gradual: a
// chunker with a subtly carry-leaking rolling hash, or with a Min floor set too close to Target,
// still passes every contiguity and size-bounds test in this package while quietly halving the
// store's dedup ratio. So these two tests run a trial distribution and report the whole histogram.

const (
	// stabilityTrials is the number of independent edit trials each direction runs.
	stabilityTrials = 512
	// stabilityBufSize is the size of each trial's original buffer: large enough for ~57 chunks
	// under the default Params, so an edit lands in the interior almost every time.
	stabilityBufSize = 256 << 10
	// stabilityMaxEdit is the largest edit, in bytes, a trial makes.
	stabilityMaxEdit = 500
	// stabilitySeed is the fixed splitmix64 seed every trial sequence is derived from. Fixed, so
	// the histogram this test logs is a property of the chunker and never of the run.
	stabilitySeed = 21

	// maxNovelPerTrial is the hard per-trial ceiling: no single 1-500 byte edit anywhere in a
	// 256 KiB buffer may introduce more than this many chunks that did not exist before. A chunker
	// that re-synchronizes at all cannot exceed a handful; this catches total desynchronization.
	maxNovelPerTrial = 12
	// pctWithinTwo and pctWithinThree are the distribution gates: the share of trials that must
	// introduce at most 2, and at most 3, novel chunks.
	//
	// These two numbers are requirements, not observations, and they are what selected the
	// normalization level this package ships. The history is recorded here because the decision is
	// invisible from fastcdc.go's side — there, NC=1 is just a constant.
	//
	// The first implementation used the NC=2 the FastCDC paper recommends, and MISSED the ≤3 gate:
	// 90.04% within 2 and 94.34% within 3 on insertion at this seed, against a required 95%.
	// Extending the same trials to 20 000 showed why that was not bad luck — the population values
	// were 90.36% / 95.02%, i.e. the gate sat exactly on the distribution's mean, so it was a coin
	// flip rather than a guard. Re-running the identical trials with only fastcdc.go's
	// maskWidthDelta varied isolated the cause to the normalization level itself:
	//
	//	NC=2   78.8% at 1   90.4% within 2   95.0% within 3   max 17
	//	NC=1   87.5% at 1   97.1% within 2   99.4% within 3   max  7
	//	NC=0   85.4% at 1   98.9% within 2   99.9% within 3   max  4
	//
	// An edit shifts the bytes after it across the Target line, where the mask changes width; a
	// boundary that fired under the lenient post-Target mask can land in the strict pre-Target
	// region and stop firing, desynchronizing the chunk after it. The wider the gap between the two
	// masks, the more often that happens.
	//
	// The resolution was to fix the implementation, not the requirement: NC was dropped to 1, and
	// these thresholds were left exactly as specified. That was the right way round because
	// 00-ARCHITECTURE.md §5.5 names boundary stability as the normative property, and because the
	// tighter size distribution NC=2 was supposed to buy does not exist on this workload — measured
	// over testdata/corpora/toolout, NC=0 through NC=3 all give the same 1 novel chunk out of 40 on
	// a single-line insertion and dedup ratios within 0.3% of each other. Both gates now pass with
	// real margin instead of sitting on the mean. See maskWidthDelta for the full rationale.
	//
	// Do not relax these to accommodate a future change to the scan. They are the reason the scan
	// looks the way it does.
	pctWithinTwo   = 85.0
	pctWithinThree = 95.0
)

// TestPropBoundaryStability_Insertion measures how many novel chunks a random insertion introduces.
func TestPropBoundaryStability_Insertion(t *testing.T) {
	t.Parallel()
	runStabilityTrials(t, true)
}

// TestPropBoundaryStability_Deletion measures the same for a random deletion. Deletion is the
// harder direction for normalized chunking and is measured separately for that reason: an insertion
// pushes the rest of the chunk later, which can only move a boundary from the strict pre-Target
// mask into the lenient post-Target one (where it still fires), whereas a deletion pulls content
// the other way and can move a boundary that fired under the lenient mask into the strict region,
// where it does not.
func TestPropBoundaryStability_Deletion(t *testing.T) {
	t.Parallel()
	runStabilityTrials(t, false)
}

// runStabilityTrials is the shared body. For each trial it builds a fresh pseudo-random buffer,
// splits it, applies one random edit of 1..stabilityMaxEdit bytes at a random offset k, splits the
// result, and checks four things:
//
//  1. Every chunk of the original that ended at or before k is present, byte-identical and at the
//     same offset, in the edited split. This one is exact, not statistical: a chunk's boundary is
//     decided left to right from the chunk's own start, so nothing at or after k can reach back and
//     change a boundary that was already placed.
//  2. A realignment index exists — after a bounded run of disturbed chunks, the tail of the edited
//     split is byte-identical to the tail of the original. This is re-synchronization itself.
//  3. No trial exceeds maxNovelPerTrial novel chunks.
//  4. The distribution gates on ≤2 and ≤3 novel chunks.
//
// A fresh buffer per trial, rather than one buffer edited 512 times, is deliberate: with a single
// fixed buffer the trials are not independent — a handful of intrinsically unstable offsets get
// resampled over and over, and the histogram reports that buffer's quirks rather than the
// chunker's behaviour.
func runStabilityTrials(t *testing.T, insert bool) {
	t.Helper()

	c := chunk.New(chunk.DefaultParams())
	r := newTestRNG(stabilitySeed)
	hist := map[int]int{}
	worst := 0

	for trial := 0; trial < stabilityTrials; trial++ {
		data := make([]byte, stabilityBufSize)
		r.fill(data)
		before := c.Split(data)
		require.Greater(t, len(before), 4, "trial %d: fixture must produce several chunks", trial)

		k := r.intn(stabilityBufSize)
		size := 1 + r.intn(stabilityMaxEdit)
		edited := applyEdit(r, data, k, size, insert)

		after := c.Split(edited)
		require.NotEmpty(t, after, "trial %d", trial)
		requireContiguous(t, after, len(edited))

		requireUntouchedPrefix(t, before, after, k, trial)
		requireRealigns(t, before, after, trial)

		novel := countNovel(before, after)
		hist[novel]++
		if novel > worst {
			worst = novel
		}
	}

	reportHistogram(t, hist, worst, insert)
}

// applyEdit returns data with size bytes inserted at k, or size bytes deleted from k. The inserted
// bytes come from the same stream as everything else, so a trial is fully described by the seed.
func applyEdit(r *testRNG, data []byte, k, size int, insert bool) []byte {
	if insert {
		ins := make([]byte, size)
		r.fill(ins)
		out := make([]byte, 0, len(data)+size)
		out = append(out, data[:k]...)
		out = append(out, ins...)
		return append(out, data[k:]...)
	}
	if k+size > len(data) {
		size = len(data) - k
	}
	if size == 0 {
		k, size = len(data)-1, 1
	}
	out := make([]byte, 0, len(data)-size)
	out = append(out, data[:k]...)
	return append(out, data[k+size:]...)
}

// requireUntouchedPrefix asserts property (1): every chunk that finished before the edit point is
// still there, unchanged and at the same offset. This is a hard per-trial assertion — a chunker
// that failed it would be reading ahead past its own boundary, which would also break SplitStream.
func requireUntouchedPrefix(t *testing.T, before, after []chunk.Chunk, k, trial int) {
	t.Helper()
	for i, c := range before {
		if int(c.Offset)+c.Len > k {
			return
		}
		require.Less(t, i, len(after), "trial %d: the edit deleted untouched chunk %d", trial, i)
		require.Equal(t, c, after[i], "trial %d: the edit changed chunk %d, which ends before it", trial, i)
	}
}

// requireRealigns asserts property (2): there is an index m after which every chunk of the edited
// split is byte-identical to a chunk of the original's tail, and m is at most maxNovelPerTrial
// chunks past the untouched prefix. That bound is what "re-synchronizes" means quantitatively —
// without it, "a realignment index exists" is satisfied trivially by m == len(after).
func requireRealigns(t *testing.T, before, after []chunk.Chunk, trial int) {
	t.Helper()

	prefix := 0
	for prefix < len(before) && prefix < len(after) && before[prefix].Hash == after[prefix].Hash {
		prefix++
	}
	suffix := 0
	for suffix < len(before)-prefix && suffix < len(after)-prefix &&
		before[len(before)-1-suffix].Hash == after[len(after)-1-suffix].Hash {
		suffix++
	}

	m := len(after) - suffix
	require.LessOrEqual(t, m-prefix, maxNovelPerTrial,
		"trial %d: the edited split never realigned — %d chunks between the untouched prefix (%d) "+
			"and the matching tail (%d of %d)", trial, m-prefix, prefix, suffix, len(after))
}

// countNovel returns how many chunks of the edited split have bytes that appear in no chunk of the
// original. Chunk hashes are content addresses, so hash-set membership is exactly "these bytes are
// already in the store" — the quantity that decides whether an edited object costs one chunk of new
// storage or a hundred.
func countNovel(before, after []chunk.Chunk) int {
	known := make(map[core.Hash]struct{}, len(before))
	for _, c := range before {
		known[c.Hash] = struct{}{}
	}
	var novel int
	for _, c := range after {
		if _, ok := known[c.Hash]; !ok {
			novel++
		}
	}
	return novel
}

// reportHistogram logs the full novelty distribution and applies the per-trial and distribution
// gates. It always logs, pass or fail: the histogram is the artifact this test produces, and a
// green run that printed nothing would make a slow regression in dedup quality invisible.
func reportHistogram(t *testing.T, hist map[int]int, worst int, insert bool) {
	t.Helper()

	kind := "insertion"
	if !insert {
		kind = "deletion"
	}

	keys := make([]int, 0, len(hist))
	for k := range hist {
		keys = append(keys, k)
	}
	sort.Ints(keys)

	pct := func(n int) float64 { return 100 * float64(n) / stabilityTrials }

	var (
		buf         bytes.Buffer
		cumulative  int
		withinOne   int
		withinTwo   int
		withinThree int
	)
	for _, k := range keys {
		cumulative += hist[k]
		if k <= 1 {
			withinOne += hist[k]
		}
		if k <= 2 {
			withinTwo += hist[k]
		}
		if k <= 3 {
			withinThree += hist[k]
		}
		fmt.Fprintf(&buf, "\n  novel=%-3d %4d trials (%5.2f%%)  cumulative %6.2f%%",
			k, hist[k], pct(hist[k]), pct(cumulative))
	}
	t.Logf("boundary stability under %s: %d trials, %d KiB buffer, edits of 1-%d bytes, seed %d%s"+
		"\n  <=1: %.2f%%   <=2: %.2f%%   <=3: %.2f%%   max: %d",
		kind, stabilityTrials, stabilityBufSize>>10, stabilityMaxEdit, stabilitySeed, buf.String(),
		pct(withinOne), pct(withinTwo), pct(withinThree), worst)

	require.LessOrEqual(t, worst, maxNovelPerTrial,
		"%s: a single edit introduced %d novel chunks, above the hard per-trial ceiling", kind, worst)
	require.GreaterOrEqual(t, pct(withinTwo), pctWithinTwo,
		"%s: only %.2f%% of trials introduced at most 2 novel chunks", kind, pct(withinTwo))
	require.GreaterOrEqual(t, pct(withinThree), pctWithinThree,
		"%s: only %.2f%% of trials introduced at most 3 novel chunks", kind, pct(withinThree))
}

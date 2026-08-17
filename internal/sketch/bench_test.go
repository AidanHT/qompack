package sketch

import (
	"fmt"
	"slices"
	"sync"
	"testing"

	"github.com/qompack/qompack/internal/core"
	"github.com/stretchr/testify/require"
)

// Micro-benchmarks for the performance budgets in the SP-03 implementation spec, which are the
// measured half of 00-ARCHITECTURE.md §13 invariant 9. There is exactly one benchmark per budget
// row and each is named for the row it defends, so a regression report names the number it broke
// rather than a function.
//
// The composite one is BenchmarkL0SketchUpdate. Qompack.md §8.1 item 5 gives the L0 observer one
// CMS update, one HLL update and one Bloom membership test per PostToolUse, and that triple is the
// whole of this package's contribution to budget B-A (hook p99 < 15 ms, 00-ARCHITECTURE.md §2.4).
// Its budget is ≤ 5 µs with zero allocations — 0.03 % of B-A — and TestL0SketchUpdate_ZeroAlloc
// asserts the allocation half separately, because an allocation on the hot path is a regression a
// nanosecond count can hide: 32 bytes per hook call is invisible in a ns/op column and is a garbage
// collector's whole working set at the daemon's call rate.
//
// MinHash is deliberately NOT on B-A. It runs in the daemon's async worker under budget B-C
// (l0_process, p99 < 50 ms, soft), which is why its two rows are budgeted in milliseconds while
// everything else here is budgeted in microseconds.
//
// Results are appended to testdata/bench-baseline.txt so benchstat gates future changes
// (00-ARCHITECTURE.md §7: > 10 % warns, > 25 % fails). Every benchmark below excludes its fixture
// construction from the timed region, and every one of them rotates over a PRE-BUILT key set:
// building a key inside the loop would allocate, and the allocation columns are the half of this
// measurement that is platform-independent and therefore the half worth gating on.

const (
	// benchBloomCapacity and benchBloomFPRate are Appendix A's sketches.bloom defaults — the
	// (10 000, 0.01) sizing that yields m = 95 872 bits, k = 7 and an 11 984-byte body. The budget
	// rows are stated against exactly this shape.
	benchBloomCapacity = 10_000
	benchBloomFPRate   = 0.01
	// benchCMSEpsilon and benchCMSDelta are Appendix A's sketches.cms defaults: 2 719 × 5 counters,
	// a 54 380-byte body.
	benchCMSEpsilon = 0.001
	benchCMSDelta   = 0.01
	// benchHLLRegisters is Appendix A's sketches.hll default register count.
	benchHLLRegisters = 2048
	// benchMGCounters is the Misra-Gries k the budget row names. The row says "saturated", so the
	// fixture pre-fills the table to k before the timer starts: an Add into a table with room is
	// one map write, and an Add into a full one is the O(k) decrement phase the budget is about.
	benchMGCounters = 256
	// benchMGSeedWeight is the weight each pre-filled counter starts at, and it is 1 because that is
	// the state a unit-weight stream actually reaches — 256 distinct keys, one Add each. It fills the
	// table at t = 0 and does not hold it there; BenchmarkMisraGriesAdd describes the cycle that
	// follows, and benchSaturatedMG why a large seed weight would measure a different quantity than
	// the one this row budgets.
	benchMGSeedWeight = 1
	// benchRebuildKeys is the key count the RebuildBloom row names — §8.3's "a linear pass over a
	// few thousand structured entries".
	benchRebuildKeys = 5000
	// benchMinHash4KiB and benchMinHash100KiB are the two MinHash input sizes. The first is below
	// MinHashSampleTarget and hashes every shingle with no selection at all; the second is above it
	// and exercises the bottom-k selector, which is the reason the two rows have different budgets
	// and the reason the second allocates where the first does not.
	benchMinHash4KiB   = 4 << 10
	benchMinHash100KiB = 100 << 10
)

// benchKeyN is the size of the rotation set every keyed benchmark draws from, and it is a power of
// two so the rotation costs one AND rather than a division. A division would be a few nanoseconds
// against budgets whose smallest row is 1 000 of them, which is not fatal — but it would be charged
// to the sketch, and this file exists to attribute cost correctly.
const (
	benchKeyN    = 1 << 13
	benchKeyMask = benchKeyN - 1
)

// benchCreated is the construction stamp the marshal fixtures use. It is a literal rather than a
// clock reading for the same reason the golden fixtures use one: a frame whose bytes depend on when
// the benchmark ran cannot be compared against itself.
const benchCreated = core.UnixMilli(1_700_000_000_000)

// benchSinkBool keeps a discarded Bloom.Test result live in TestL0SketchUpdate_ZeroAlloc, and
// nowhere else.
//
// The benchmarks below carry no sinks. b.Loop's documented Go 1.24 guarantee is that the loop body
// is not optimised away, so a sink there would buy nothing and cost one store per iteration —
// charged to the sketch, in a file whose whole purpose is attributing cost correctly. That was
// verified rather than assumed: removing the sinks moved no row outside its confidence interval,
// where an eliminated call would have collapsed to sub-nanosecond.
//
// The test is the exception because testing.AllocsPerRun is not b.Loop and carries no such
// guarantee. An eliminated call there would report zero allocations and pass vacuously, which is
// the one failure that assertion exists to prevent. internal/core/hash_test.go keeps sinkHash for
// exactly the same reason.
var benchSinkBool bool

// benchPathKeys returns benchKeyN path-shaped keys — what SP-08 passes as []byte(paths.Key(path)).
// They are built once for the whole binary: a benchmark's fixture is outside its timed region
// either way, but -count=6 over fifteen benchmarks would otherwise rebuild them ninety times.
var benchPathKeys = sync.OnceValue(func() [][]byte {
	keys := make([][]byte, benchKeyN)
	for i := range keys {
		keys[i] = []byte(fmt.Sprintf("src/internal/pkg%03d/service_%04d.go", i%128, i))
	}
	return keys
})

// benchDescriptorKeys returns benchKeyN negative-knowledge descriptor keys. SP-09 passes
// negknow.Descriptor.Key(), which is itself a core.HashBytes digest, so these are 32 bytes of
// digest rather than a path: Bloom.Test hashes whatever it is handed, and measuring it on a short
// path would understate the key length the one caller that exists will actually use.
var benchDescriptorKeys = sync.OnceValue(func() [][]byte {
	keys := make([][]byte, benchKeyN)
	for i := range keys {
		h := core.HashBytes(core.DomainNegKnow, []byte(fmt.Sprintf("approach-%d", i)))
		keys[i] = h[:]
	}
	return keys
})

// benchMGKeys returns benchKeyN Misra-Gries keys. They are strings, not []byte, because that is
// MisraGries.Add's parameter type; converting a []byte at the call site would allocate on every
// iteration and charge the conversion to the summary.
var benchMGKeys = sync.OnceValue(func() []string {
	keys := make([]string, benchKeyN)
	for i := range keys {
		keys[i] = fmt.Sprintf("Bash:src/internal/pkg%03d/service_%04d.go", i%128, i)
	}
	return keys
})

// benchFilledBloom returns a filter holding all benchKeyN path keys — 8 192 of a configured
// capacity of 10 000, so a fill ratio of ~0.45 rather than the ~0.52 of a filter at capacity.
//
// What matters to the rows that use it is that every key they probe is present. Occupancy decides
// how much of Test runs: a miss returns at the first clear bit, a hit reads all seven positions, so
// the conservative measurement of the budget row — the one a budget model should be given — is the
// one where every probe is a hit.
func benchFilledBloom() *Bloom {
	b := NewBloom(benchBloomCapacity, benchBloomFPRate)
	b.SetCreated(benchCreated)
	for _, k := range benchPathKeys() {
		b.Add(k)
	}
	return b
}

// benchDescriptorBloom returns a filter holding all benchKeyN DESCRIPTOR keys, which is the only
// fixture that resembles the real sketches/tried.bloom: §8.3's filter holds negative-knowledge
// descriptors, never paths. BenchmarkL0SketchUpdate needs its own filter rather than sharing
// benchFilledBloom's, because adding 8 192 descriptors on top of 8 192 paths would put that filter
// past its configured capacity of 10 000 and move BenchmarkBloomTest, a row that is currently
// correct.
func benchDescriptorBloom() *Bloom {
	b := NewBloom(benchBloomCapacity, benchBloomFPRate)
	b.SetCreated(benchCreated)
	for _, k := range benchDescriptorKeys() {
		b.Add(k)
	}
	return b
}

// benchFilledCMS returns a Count-Min sketch carrying one weighted pass over the rotation set, so
// Estimate reads populated counters rather than a table of zeros.
func benchFilledCMS() *CMS {
	c := NewCMS(benchCMSEpsilon, benchCMSDelta)
	c.SetCreated(benchCreated)
	for _, k := range benchPathKeys() {
		c.Add(k, 1)
	}
	return c
}

// benchFilledHLL returns a HyperLogLog carrying one pass over the rotation set. Cardinality's cost
// is a loop over every register whatever they hold, but a filled sketch takes the harmonic-mean
// branch rather than the linear-counting fallback, and that is the branch production reads.
func benchFilledHLL() *HLL {
	h := NewHLL(benchHLLRegisters)
	h.SetCreated(benchCreated)
	for _, k := range benchPathKeys() {
		h.Add(k)
	}
	return h
}

// benchSaturatedMG returns a Misra-Gries summary whose table is full at t = 0 — the state the budget
// row names — reached the way the algorithm reaches it, by one unit Add of each of k distinct keys.
// It does not STAY full; see BenchmarkMisraGriesAdd for the cycle that follows and why that cycle is
// the quantity the row budgets.
//
// The seed weight is the whole design of this fixture and it was chosen against a measurement, not
// by taste. A decrement pass subtracts d = min(n, smallest counter), which is 1 for a unit Add, and
// deletes every counter it drives to zero. Pre-loading the table with a large weight instead — 1<<24
// was tried — leaves counters that no run can drain, so the table stays permanently full and EVERY
// miss pays the two O(k) map passes: that measured 7.9 µs/op in a one-off probe on the pre-fix
// recording, not among the committed samples, and it would record a 1.6× miss of a 5 µs budget that
// the code does not actually incur. It is the wrong quantity, because the standard
// Misra-Gries potential argument (Φ = total counter mass; a pass costs O(k) and drops Φ by k·d,
// while an Add raises it by n) says a pass can happen at most once per k unit Adds in any stream
// that BUILT its own table. Seeding with mass the stream never supplied suspends exactly that bound.
//
// Seeded at weight 1 the summary reaches its true steady state — one O(k) pass per ~k Adds — after a
// transient of a few hundred iterations, which a one-second sample dwarfs. That amortized figure is
// what the row's "≤ 5 µs/op amortized" budgets.
func benchSaturatedMG() *MisraGries {
	m := NewMisraGries(benchMGCounters)
	m.SetCreated(benchCreated)
	for i := 0; i < benchMGCounters; i++ {
		m.Add(benchMGKeys()[i], benchMGSeedWeight)
	}
	return m
}

// benchFrame marshals s and fails the benchmark if it cannot, so an unmarshal benchmark never
// measures a decoder against a nil buffer.
func benchFrame(b *testing.B, s Sketch) []byte {
	b.Helper()
	frame, err := s.MarshalBinary()
	require.NoError(b, err)
	return frame
}

// BenchmarkBloomAdd measures Bloom.Add at the Appendix A sizing: one domain-separated SHA-256 plus
// seven probes. Budget: ≤ 1.0 µs/op, 0 allocs/op.
//
// It is seven bit READS and a conditional write, not seven writes. The rotation set is already in
// the filter and re-added, so after the first pass every probed bit is set, the `words[w] |= bit`
// branch is never taken and satAdd64 never runs. That is why Add measures slightly BELOW Test while
// nominally doing more work — the two are the same seven memory reads, and Add pays a predictable
// branch where Test pays an early-exit test. Measuring Add on novel keys instead would need a key
// generated inside the timed region, which allocates, and would charge that allocation to the
// filter.
func BenchmarkBloomAdd(b *testing.B) {
	filter := benchFilledBloom()
	keys := benchPathKeys()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; b.Loop(); i++ {
		filter.Add(keys[i&benchKeyMask])
	}
}

// BenchmarkBloomTest measures Bloom.Test where every probe is a HIT, so all seven bit reads are
// performed rather than short-circuiting at the first clear one. Budget: ≤ 1.0 µs/op, 0 allocs/op.
func BenchmarkBloomTest(b *testing.B) {
	filter := benchFilledBloom()
	keys := benchPathKeys()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; b.Loop(); i++ {
		filter.Test(keys[i&benchKeyMask])
	}
}

// BenchmarkCMSAdd measures CMS.Add at ε = 0.001, δ = 0.01 — five rows.
// Budget: ≤ 1.0 µs/op, 0 allocs/op.
func BenchmarkCMSAdd(b *testing.B) {
	c := benchFilledCMS()
	keys := benchPathKeys()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; b.Loop(); i++ {
		c.Add(keys[i&benchKeyMask], 1)
	}
}

// BenchmarkCMSEstimate measures the five-row minimum. Budget: ≤ 1.0 µs/op, 0 allocs/op.
func BenchmarkCMSEstimate(b *testing.B) {
	c := benchFilledCMS()
	keys := benchPathKeys()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; b.Loop(); i++ {
		c.Estimate(keys[i&benchKeyMask])
	}
}

// BenchmarkHLLAdd measures HLL.Add over 2 048 registers: one SHA-256, one leading-zero count and
// one conditional max. Budget: ≤ 1.0 µs/op, 0 allocs/op.
func BenchmarkHLLAdd(b *testing.B) {
	h := benchFilledHLL()
	keys := benchPathKeys()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; b.Loop(); i++ {
		h.Add(keys[i&benchKeyMask])
	}
}

// BenchmarkHLLCardinality measures the estimator: 2 048 Ldexp accumulations plus the bias
// correction. Budget: ≤ 25 µs/op. It is not on the hook path — SP-14's /qompack:status reads it —
// which is why it is budgeted in tens of microseconds rather than in one.
func BenchmarkHLLCardinality(b *testing.B) {
	h := benchFilledHLL()
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		h.Cardinality()
	}
}

// BenchmarkMisraGriesAdd measures Add into a k = 256 summary CYCLING through saturation, which is
// what a unit-weight stream of mostly-novel keys actually does to Misra-Gries. Budget: ≤ 5 µs/op
// amortized.
//
// The table is full only at the moment of a decrement pass. Every counter holds 1, so d = 1 wipes
// all 256 at once; the next 256 Adds then refill it one key at a time down the has-room fast path,
// and the 257th pays the two O(k) map passes again. About one iteration in 257 sees a full table.
// That cycle is not a fixture artefact — it is the algorithm's steady state under this stream, and
// it is exactly what the Misra-Gries potential bound predicts (a pass costs O(k) and drops the
// counter mass by k·d, while an Add raises it by n, so passes cannot exceed one per k unit Adds).
//
// The "amortized" in the budget is therefore load-bearing rather than decorative: one decrement
// pass in isolation measured 7.9 µs on this host — 1.6× the whole row — in a one-off probe run
// recorded in the task-8 report, not among the committed samples. What keeps the row met is the
// bound above, and benchSaturatedMG documents why a fixture that suspends it measures a cost the
// code cannot be driven into.
//
// Misra-Gries is NOT part of the composite hot path. §8.1 item 5 gives the observer Count-Min and
// HyperLogLog; the summary is fed on the daemon side, under B-C.
func BenchmarkMisraGriesAdd(b *testing.B) {
	m := benchSaturatedMG()
	keys := benchMGKeys()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; b.Loop(); i++ {
		m.Add(keys[i&benchKeyMask], 1)
	}
}

// BenchmarkL0SketchUpdate is the number this subplan owes the L0 model: the entire §8.1 item 5
// contribution to one PostToolUse, which is one CMS.Add, one HLL.Add and one Bloom.Test.
// Budget: ≤ 5 µs/op, 0 allocs/op — 0.03 % of the 15 ms B-A hook budget.
//
// The path key and the descriptor key are deliberately different values: the observer counts a file
// touch and asks the negative-knowledge filter about an approach descriptor, and those are two
// different keys in production. Feeding one key to all three calls would let a warm cache line do
// work the real hook does not get for free.
//
// # Why the Bloom probe is a hit, and what a miss actually measures
//
// The filter is benchDescriptorBloom, pre-filled with the very descriptors this loop probes, so
// every Test reads all seven positions. That is the conservative reading, and it is also the only
// fixture that resembles production in KIND: sketches/tried.bloom holds negative-knowledge
// descriptors (§8.3), never paths, so a filter built from path keys was not the production filter
// at all — every descriptor probe missed it by construction.
//
// In production most probes DO miss, because most descriptors have not been eliminated, and a miss
// returns at the first clear bit rather than reading all seven. So this row reports the less common
// case on purpose: a budget-model input should be the conservative number.
//
// What that choice is worth was measured rather than assumed, in a one-off probe run (count=12,
// hit and miss variants side by side, not among the committed samples): the composite measured
// 498.0n ±7 % with a hit and 486.5n ±3 % with a miss, and Bloom.Test alone 174.7n ±5 % versus
// 166.2n ±5 %. The short-circuit is worth ~8–12 ns, about 2 % of the composite — Test's cost is
// dominated by the one SHA-256 in hash128, not by the seven probes it guards. Note what that rules
// out: an earlier recording of the miss fixture sat 20 % below the sum of its three component rows,
// and that gap was NOT the short-circuit. It was run-to-run noise on this host, which is the whole
// argument for judging these rows with benchstat over repeated samples.
func BenchmarkL0SketchUpdate(b *testing.B) {
	c := benchFilledCMS()
	h := benchFilledHLL()
	filter := benchDescriptorBloom()
	pathKeys := benchPathKeys()
	descs := benchDescriptorKeys()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; b.Loop(); i++ {
		k := pathKeys[i&benchKeyMask]
		c.Add(k, 1)
		h.Add(k)
		filter.Test(descs[i&benchKeyMask])
	}
}

// TestL0SketchUpdate_ZeroAlloc holds the composite hot path to zero allocations, which is the half
// of its budget a ns/op column cannot show. It is a test rather than a benchmark assertion because
// benchmarks do not run in CI's default `go test` invocation, and a hot-path allocation that only a
// nightly benchmark can see is a regression that ships.
func TestL0SketchUpdate_ZeroAlloc(t *testing.T) {
	c := benchFilledCMS()
	h := benchFilledHLL()
	filter := benchDescriptorBloom()
	key := benchPathKeys()[0]
	desc := benchDescriptorKeys()[0]

	got := testing.AllocsPerRun(1000, func() {
		c.Add(key, 1)
		h.Add(key)
		benchSinkBool = filter.Test(desc)
	})

	require.Zerof(t, got, "the §8.1 item 5 hot path allocated %v times per PostToolUse; its budget "+
		"is 0. Each of the three calls makes exactly one core.HashBytes call, so a result of 3 means "+
		"the allocation is inside core.HashBytes rather than in this package", got)
}

// BenchmarkMinHash4KiB measures a 4 KiB document at 128 permutations: ≈ 4 089 shingles, below
// MinHashSampleTarget, so every shingle is permuted and no selection runs. It is the row that pins
// the small-document path at two allocations — the minima and the coefficient table, nothing else —
// since the selector's structures are allocated only above the target and the hashing batch stays
// on the stack.
// Budget: ≤ 1.5 ms/op. This is a B-C measurement, not B-A — MinHash runs in the daemon's async
// worker (l0_process, p99 < 50 ms, soft), never in the hook.
func BenchmarkMinHash4KiB(b *testing.B) {
	doc := mhDoc(mhSeedA, benchMinHash4KiB)
	opts := mhOptions()
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		MinHash(doc, opts)
	}
}

// BenchmarkMinHash100KiB measures a 100 KiB document at 128 permutations. Bottom-k keeps exactly
// MinHashSampleTarget distinct hashes, so the permutation work is flat in the document size and only
// the FNV pass over the input grows. Budget: ≤ 2.5 ms/op, also under B-C.
//
// Its allocation figure is flat in the document size too, and deliberately so: the selector's table
// and buffer are sized from MinHashSampleTarget, so a 1 MiB input allocates the same ~396 KiB this
// row reports. The row carries no allocation budget — testdata/bench-baseline.txt names the six
// that do — but the number is worth watching for the same reason, since a size that tracked the
// document would be a different algorithm.
//
// This row is the noisiest in the file: the identical body has been measured swinging between
// 1.69 ms and 2.83 ms on the development host depending on machine load. Judge it with benchstat
// over at least six samples, never a single run.
func BenchmarkMinHash100KiB(b *testing.B) {
	doc := mhDoc(mhSeedB, benchMinHash100KiB)
	opts := mhOptions()
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		MinHash(doc, opts)
	}
}

// BenchmarkBloomMarshal measures encoding an 11 984-byte body: the header, four params, a memcpy of
// the word array and one CRC32C pass. Budget: ≤ 60 µs/op. This is checkpoint work (§8.5), not hook
// work.
func BenchmarkBloomMarshal(b *testing.B) {
	filter := benchFilledBloom()
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, err := filter.MarshalBinary(); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkBloomUnmarshal measures decoding the same 11 984-byte body, CRC verification included.
// Budget: ≤ 60 µs/op. The destination is reused across iterations because UnmarshalBinary replaces
// the receiver's whole state; allocating a fresh *Bloom per iteration would charge the decoder for
// a constructor the caller does not run.
func BenchmarkBloomUnmarshal(b *testing.B) {
	frame := benchFrame(b, benchFilledBloom())
	dst := new(Bloom)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if err := dst.UnmarshalBinary(frame); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkCMSMarshal measures encoding a 54 380-byte body. Budget: ≤ 250 µs/op — 4.5× the Bloom
// row for 4.5× the bytes, because both are memcpy plus CRC32C and neither is hashing.
func BenchmarkCMSMarshal(b *testing.B) {
	c := benchFilledCMS()
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, err := c.MarshalBinary(); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkCMSUnmarshal measures decoding the same 54 380-byte body. Budget: ≤ 250 µs/op.
func BenchmarkCMSUnmarshal(b *testing.B) {
	frame := benchFrame(b, benchFilledCMS())
	dst := new(CMS)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if err := dst.UnmarshalBinary(frame); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkRebuildBloom5000 measures §8.3's rebuild: a fresh filter at capacity 10 000 populated
// from an iterator of 5 000 eliminated-approach keys. Budget: ≤ 15 ms/op — §8.3 calls it "cheap …
// a linear pass over a few thousand structured entries", and this benchmark is what makes that
// adjective a number. It runs on the rebuildOnStale path (Appendix C eliminations.rebuildOnStale),
// which is idle-time work, never hook work.
func BenchmarkRebuildBloom5000(b *testing.B) {
	keys := benchPathKeys()[:benchRebuildKeys]
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		RebuildBloom(benchBloomCapacity, benchBloomFPRate, slices.Values(keys))
	}
}

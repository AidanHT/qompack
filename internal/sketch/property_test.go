package sketch

import (
	"bytes"
	"encoding/binary"
	"iter"
	"sync"
	"testing"

	"github.com/qompack/qompack/internal/core"
	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"
)

// These properties are package sketch, not package sketch_test, for the reason header_test.go
// gives: TestProp_HLLMergeIsRegisterMax compares the unexported regs arrays byte for byte, which
// is the only statement of "the merge is a register-wise maximum" that cannot be satisfied by an
// estimator that happens to agree. internal/testutil imports internal/store, which imports this
// package, so an in-package _test.go may never import testutil — nothing here does.
//
// Every bound below is chosen from the NUMBER OF DRAWS rather than from one draw. rapid runs 100
// draws per invocation by default, so a bound with a 1-in-300 per-draw tail fails the suite about
// a quarter of the time on a correct implementation. Where a property is statistical rather than
// structural (TestProp_MinHashJaccardAccuracy is the only one), the tolerance is stated at 4.5
// standard errors and its arithmetic is written out at the assertion.

const (
	// propKeyMinLen and propKeyMaxLen are the plan's "len 1–64" key draw. Keys are opaque bytes to
	// this package, so nothing here normalizes them.
	propKeyMinLen = 1
	propKeyMaxLen = 64
	// propKeyMinCount and propKeyMaxCount are the plan's "1–500 distinct byte slices".
	propKeyMinCount = 1
	propKeyMaxCount = 500
	// propBloomCapacity sizes every Bloom drawn here comfortably above propKeyMaxCount, so that a
	// false-negative found by this property is a real one and not a saturated filter.
	propBloomCapacity = 4096
	// propBloomFPRate is Appendix C's configured rate; the property is one-sided, so the exact
	// value only decides how many bits get allocated.
	propBloomFPRate = 0.01
)

const (
	// propCMSEpsilon and propCMSDelta size the Count-Min tables. They are loose relative to
	// Appendix C (ε = 0.001) on purpose: a 100×-smaller table makes collisions COMMON, which is
	// what turns "Estimate never under-counts" into a property that could actually fail if the
	// estimator ever took a minimum over the wrong rows.
	propCMSEpsilon = 0.05
	propCMSDelta   = 0.05
	// propWeightMin and propWeightMax are the plan's "weight ∈ [1,1000]". The heaviest stream any
	// property below can draw therefore carries 500 000 — six orders of magnitude below the uint32
	// counter ceiling and thirteen below Misra-Gries's — so no assertion has to reason about
	// saturation.
	propWeightMin = 1
	propWeightMax = 1000
	// propCMSStreamMax and propMGStreamMax are the plan's two stream bounds, which differ: line
	// 1219 gives Count-Min "≤ 300 (key, weight) pairs" and line 1222 gives Misra-Gries "stream
	// ≤ 500 items". They are separate constants rather than one shared minimum because the numbers
	// are requirements rather than conveniences — collapsing them to 300 would silently narrow the
	// Misra-Gries draw, and the decrement phase is exactly what a longer stream buys.
	propCMSStreamMax = 300
	propMGStreamMax  = 500
)

const (
	// propHLLRegisters is the register count every HyperLogLog property uses. It is the smallest
	// legal one (MinHLLRegisters) rather than Appendix C's 2 048, because these properties are
	// about the register ALGEBRA — merge is a register-wise maximum — and a small array makes the
	// drawn key sets actually collide on registers, where 2 048 would leave most of them untouched
	// and the comparison would pass on a merge that only handled the sparse case.
	propHLLRegisters = MinHLLRegisters
	// propMGMinK and propMGMaxK are the plan's "k ∈ [1,32]" counter budget.
	propMGMinK = 1
	propMGMaxK = 32
)

const (
	// propBodyMax is the plan's "arbitrary body ≤ 4 KiB" for TestProp_HeaderRoundTrip and the
	// "arbitrary []byte ≤ 4 KiB" for TestProp_UnmarshalNeverPanics.
	propBodyMax = 4096
	// propParamMax is the header's own maxParams. Drawing right up to the limit is deliberate: the
	// encoder rejects at 65, so 64 is the value most likely to catch an off-by-one.
	propParamMax = maxParams
	// propParamValueBound keeps drawn param values FINITE. math.Float64bits round-trips a NaN
	// exactly, but require.Equal reports two NaNs as unequal, so a drawn NaN would fail a correct
	// encoder for a reason that has nothing to do with the encoding.
	propParamValueBound = 1e300
)

const (
	// propDocMin and propDocMax bound each SEGMENT of a drawn MinHash document — the shared prefix
	// and the differing tail — not the document itself. A document is one of each, so it runs to at
	// most 4 KiB, which is the figure that matters: 4 096 bytes yield 4 089 shingle positions, below
	// MinHashSampleTarget's 8 192, so the content-defined sampler keeps every shingle and the
	// estimator's standard error is the textbook 1/√P rather than the sampled one. propDocMin keeps
	// each tail well above the shingle width, so the shingle sets are never degenerate.
	propDocMin = 256
	propDocMax = 2048
	// propMinHashPerms is Appendix C's 128 permutations.
	propMinHashPerms = 128
	// propMinHashShingle is the default 8-byte shingle.
	propMinHashShingle = 8
	// propJaccardTolerance is the accuracy bound of TestProp_MinHashJaccardAccuracy. See that
	// test's comment: it is 4.5 standard errors and must not be tightened.
	propJaccardTolerance = 0.20
)

// propKeys is the shared key-set generator: 1–500 distinct byte slices of 1–64 bytes.
func propKeys() *rapid.Generator[[][]byte] {
	return rapid.SliceOfNDistinct(
		rapid.SliceOfN(rapid.Byte(), propKeyMinLen, propKeyMaxLen),
		propKeyMinCount, propKeyMaxCount,
		func(k []byte) string { return string(k) },
	)
}

// propWeightedStream is the shared (key, weight) stream generator, bounded at maxLen items — the
// plan gives Count-Min and Misra-Gries different limits, so the bound is a parameter rather than a
// package constant.
//
// Keys are drawn as [a-p]{1,4} rather than freely, so a drawn stream actually REPEATS keys. A stream
// of entirely distinct keys would never reach Misra-Gries's decrement phase, and every count would
// then be exact for a reason that has nothing to do with the algorithm being right.
func propWeightedStream(maxLen int) *rapid.Generator[[]propWeighted] {
	return rapid.SliceOfN(rapid.Custom(func(rt *rapid.T) propWeighted {
		return propWeighted{
			key:    rapid.StringMatching(`[a-p]{1,4}`).Draw(rt, "key"),
			weight: rapid.IntRange(propWeightMin, propWeightMax).Draw(rt, "weight"),
		}
	}), 0, maxLen)
}

// propWeighted is one (key, weight) pair of a drawn stream.
type propWeighted struct {
	key    string
	weight int
}

// propSeq adapts a drawn key set to the iter.Seq[[]byte] RebuildBloom consumes.
func propSeq(keys [][]byte) iter.Seq[[]byte] {
	return func(yield func([]byte) bool) {
		for _, k := range keys {
			if !yield(k) {
				return
			}
		}
	}
}

// TestProp_BloomNoFalseNegatives is the Bloom filter's defining guarantee (00-ARCHITECTURE.md
// §5.7) stated over arbitrary key sets rather than over one fixture: a filter may report a key it
// never saw, but it must never fail to report one it did. §13 invariant 3 lets every consumer
// treat a `false` answer as authoritative — negknow (SP-09) skips the record lookup entirely on a
// miss — so a single false negative is a correctness bug in everything downstream, not a degraded
// estimate.
func TestProp_BloomNoFalseNegatives(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		keys := propKeys().Draw(rt, "keys")

		b := NewBloom(propBloomCapacity, propBloomFPRate)
		for _, k := range keys {
			b.Add(k)
		}
		for _, k := range keys {
			require.True(rt, b.Test(k),
				"Bloom.Test reported false for a key it was given to Add: %x", k)
		}
	})
}

// TestProp_BloomRebuildEquivalence pins the equivalence §12's resize mitigation rests on: a filter
// rebuilt in one pass from the eliminated[] set must answer exactly as the filter that was built
// incrementally. If the two ever disagreed, a resize would silently change what the agent believes
// it has already tried — losing negative knowledge at precisely the moment the system decided it
// needed more of it.
//
// The probe keys are drawn as well as the added ones, so the comparison covers the false-POSITIVE
// pattern too: two filters that agreed only on the added keys could still differ on everything
// else, which would make a rebuild observable to a caller.
func TestProp_BloomRebuildEquivalence(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		keys := propKeys().Draw(rt, "keys")
		probes := propKeys().Draw(rt, "probes")

		incremental := NewBloom(propBloomCapacity, propBloomFPRate)
		for _, k := range keys {
			incremental.Add(k)
		}
		rebuilt := RebuildBloom(propBloomCapacity, propBloomFPRate, propSeq(keys))

		gotM, gotK := rebuilt.Bits()
		wantM, wantK := incremental.Bits()
		require.Equal(rt, wantM, gotM, "RebuildBloom must size the filter identically")
		require.Equal(rt, wantK, gotK, "RebuildBloom must probe the filter identically")

		for _, k := range append(append([][]byte(nil), keys...), probes...) {
			require.Equal(rt, incremental.Test(k), rebuilt.Test(k),
				"RebuildBloom and an incremental build disagree on %x", k)
		}
	})
}

// TestProp_CMSOverestimateOnly states the Count-Min sketch's one-sided guarantee over arbitrary
// streams: Estimate may over-count, from collisions, but it may never under-count. That direction
// is what makes the sketch safe for the scheduler to act on — a high estimate is evidence to
// check, where an under-count would let a hot file read as cold with nothing downstream able to
// tell (§5.7).
//
// The bound is asserted PER KEY rather than as a 99 % fraction, because "never under-counts" is
// the deterministic half of the guarantee: it follows from every row being incremented and
// Estimate taking a minimum, and holds for every key of every stream. The probabilistic half — the
// ε·N over-count bound at 1 − δ — is the one that has to be stated as a fraction, and cms_test.go's
// TestCMS_ErrorBoundHolds owns it.
func TestProp_CMSOverestimateOnly(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		stream := propWeightedStream(propCMSStreamMax).Draw(rt, "stream")

		c := NewCMS(propCMSEpsilon, propCMSDelta)
		truth := map[string]uint32{}
		var total uint64
		for _, it := range stream {
			c.Add([]byte(it.key), uint32(it.weight))
			truth[it.key] += uint32(it.weight)
			total += uint64(it.weight)
		}

		require.Equal(rt, total, c.Total(), "Total must be the sum of every weight added")
		for k, want := range truth {
			require.GreaterOrEqual(rt, c.Estimate([]byte(k)), want,
				"CMS.Estimate under-counted %q (true count %d)", k, want)
		}
	})
}

// TestProp_CMSMergeAdditive is the warm-start invariant (Phase 7 O4, §5.7): merging a historical
// table into a live one must give exactly the table a single pass over both streams would have
// produced. Exactness — not approximation — is the claim, because a Count-Min cell is a plain
// counter and merge is cell-wise addition; anything looser would mean the merge had lost or
// duplicated mass, and every ε·N bound stated against Total would then be stated against the wrong
// N.
//
// "absent saturation" is guaranteed by construction here rather than assumed: the drawn weights
// bound the combined stream mass at 600 000, six orders of magnitude below the uint32 ceiling.
func TestProp_CMSMergeAdditive(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		streamA := propWeightedStream(propCMSStreamMax).Draw(rt, "streamA")
		streamB := propWeightedStream(propCMSStreamMax).Draw(rt, "streamB")

		merged := NewCMS(propCMSEpsilon, propCMSDelta)
		other := NewCMS(propCMSEpsilon, propCMSDelta)
		singlePass := NewCMS(propCMSEpsilon, propCMSDelta)
		keys := map[string]struct{}{}
		for _, it := range streamA {
			merged.Add([]byte(it.key), uint32(it.weight))
			singlePass.Add([]byte(it.key), uint32(it.weight))
			keys[it.key] = struct{}{}
		}
		for _, it := range streamB {
			other.Add([]byte(it.key), uint32(it.weight))
			singlePass.Add([]byte(it.key), uint32(it.weight))
			keys[it.key] = struct{}{}
		}

		require.NoError(rt, merged.MergeFrom(other))
		require.Equal(rt, singlePass.Total(), merged.Total(),
			"a merge must carry the summed stream mass, not one side's")
		require.Equal(rt, singlePass.cells, merged.cells,
			"MergeFrom must be cell-wise addition: the merged table must equal the single-pass table")
		for k := range keys {
			require.Equal(rt, singlePass.Estimate([]byte(k)), merged.Estimate([]byte(k)),
				"merged and single-pass estimates disagree on %q", k)
		}
	})
}

// TestProp_HLLMergeIsRegisterMax states the HyperLogLog merge exactly, in registers rather than in
// estimates: merging two sketches must produce the register array of the UNION of their key sets,
// byte for byte. Asserting on Cardinality instead would be a much weaker claim — two register
// arrays that differ in a handful of positions produce estimates well within the 2.3 % standard
// error, so a merge that dropped registers would pass an estimate-level test and quietly lose
// exploration history at every session boundary (§5.7, "explore.hll").
func TestProp_HLLMergeIsRegisterMax(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		keysA := propKeys().Draw(rt, "keysA")
		keysB := propKeys().Draw(rt, "keysB")

		left := NewHLL(propHLLRegisters)
		right := NewHLL(propHLLRegisters)
		union := NewHLL(propHLLRegisters)
		for _, k := range keysA {
			left.Add(k)
			union.Add(k)
		}
		for _, k := range keysB {
			right.Add(k)
			union.Add(k)
		}

		require.NoError(rt, left.MergeFrom(right))
		require.Equal(rt, union.regs, left.regs,
			"MergeFrom must be a register-wise maximum: the merged registers must equal the union's")
	})
}

// TestProp_MGNoFalsePositives states all three halves of the Misra-Gries contract at once, over
// arbitrary streams: every reported key was really added (no false positives), every reported count
// is a lower bound on the truth, and the gap between them is within the summary's own declared
// MaxError — which is itself within total/(k+1).
//
// The last inequality is the whole source of the retention guarantee TestProp_MGFrequentItemsRetained
// asserts, so pinning it here is what makes that test a consequence rather than a coincidence. The
// drawn keys stay far below MaxMGKeyBytes, so nothing here is comparing a truncated key against an
// untruncated one.
func TestProp_MGNoFalsePositives(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		k := rapid.IntRange(propMGMinK, propMGMaxK).Draw(rt, "k")
		stream := propWeightedStream(propMGStreamMax).Draw(rt, "stream")

		m := NewMisraGries(k)
		truth := map[string]int{}
		for _, it := range stream {
			m.Add(it.key, it.weight)
			truth[it.key] += it.weight
		}

		require.LessOrEqual(rt, m.MaxError(), m.Total()/int64(k+1),
			"the accumulated decrement must stay within total/(k+1)")

		top := m.Top(0)
		require.LessOrEqual(rt, len(top), k, "the summary must never hold more than k counters")
		for _, c := range top {
			trueCount, wasAdded := truth[c.Key]
			require.True(rt, wasAdded, "Top reported %q, which was never Added", c.Key)
			require.Positive(rt, c.Count, "Top reported %q with a non-positive count", c.Key)
			require.LessOrEqual(rt, c.Count, trueCount,
				"Top over-reported %q: %d > the true count %d", c.Key, c.Count, trueCount)
			require.LessOrEqual(rt, int64(trueCount-c.Count), m.MaxError(),
				"Top under-reported %q by %d, more than MaxError() = %d",
				c.Key, trueCount-c.Count, m.MaxError())
		}
	})
}

// TestProp_MGFrequentItemsRetained is the other side of the summary's contract: absence is not
// evidence in general, but it IS evidence above the retention threshold. Every key whose true count
// exceeds total/(k+1) must survive in Top(k), because err ≤ total/(k+1) and a reported count is at
// least true − err. This is the property SP-08's "hottest files" list depends on — without it a
// genuinely hot path could be evicted by a long tail of one-off touches and never reappear.
func TestProp_MGFrequentItemsRetained(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		k := rapid.IntRange(propMGMinK, propMGMaxK).Draw(rt, "k")
		stream := propWeightedStream(propMGStreamMax).Draw(rt, "stream")

		m := NewMisraGries(k)
		truth := map[string]int{}
		for _, it := range stream {
			m.Add(it.key, it.weight)
			truth[it.key] += it.weight
		}

		reported := map[string]int{}
		for _, c := range m.Top(k) {
			reported[c.Key] = c.Count
		}

		threshold := m.Total() / int64(k+1)
		for key, trueCount := range truth {
			if int64(trueCount) <= threshold {
				continue
			}
			_, kept := reported[key]
			require.True(rt, kept,
				"%q has a true count of %d, above the retention threshold total/(k+1) = %d, "+
					"but Top(%d) dropped it", key, trueCount, threshold, k)
		}
	})
}

// TestProp_MarshalIdempotent is the on-disk stability claim of §6.2 stated as an algebraic
// identity: marshal ∘ unmarshal ∘ marshal == marshal, byte for byte, for all five sketches under
// arbitrary operation streams.
//
// Byte equality rather than value equality is the point. Two encodings of the same logical state
// would both decode correctly and would still break the content-addressed store (SP-06), which
// keys on the bytes, and the frozen golden fixtures, which compare them. Go randomizes map
// iteration order deliberately, so a canonical encoding is something the encoder has to establish
// rather than something it inherits — this property is what says it did.
func TestProp_MarshalIdempotent(t *testing.T) {
	t.Run("bloom", func(t *testing.T) {
		rapid.Check(t, func(rt *rapid.T) {
			keys := propKeys().Draw(rt, "keys")
			b := NewBloom(propBloomCapacity, propBloomFPRate)
			b.SetCreated(propCreated(rt))
			for _, k := range keys {
				b.Add(k)
			}
			propRequireStableEncoding(rt, b, func() Sketch { return NewBloom(propBloomCapacity, propBloomFPRate) })
		})
	})

	t.Run("cms", func(t *testing.T) {
		rapid.Check(t, func(rt *rapid.T) {
			stream := propWeightedStream(propCMSStreamMax).Draw(rt, "stream")
			c := NewCMS(propCMSEpsilon, propCMSDelta)
			c.SetCreated(propCreated(rt))
			for _, it := range stream {
				c.Add([]byte(it.key), uint32(it.weight))
			}
			propRequireStableEncoding(rt, c, func() Sketch { return NewCMS(propCMSEpsilon, propCMSDelta) })
		})
	})

	t.Run("hll", func(t *testing.T) {
		rapid.Check(t, func(rt *rapid.T) {
			keys := propKeys().Draw(rt, "keys")
			h := NewHLL(propHLLRegisters)
			h.SetCreated(propCreated(rt))
			for _, k := range keys {
				h.Add(k)
			}
			propRequireStableEncoding(rt, h, func() Sketch { return NewHLL(propHLLRegisters) })
		})
	})

	t.Run("misragries", func(t *testing.T) {
		rapid.Check(t, func(rt *rapid.T) {
			k := rapid.IntRange(propMGMinK, propMGMaxK).Draw(rt, "k")
			stream := propWeightedStream(propMGStreamMax).Draw(rt, "stream")
			m := NewMisraGries(k)
			m.SetCreated(propCreated(rt))
			for _, it := range stream {
				m.Add(it.key, it.weight)
			}
			propRequireStableEncoding(rt, m, func() Sketch { return NewMisraGries(k) })
		})
	})

	t.Run("minhash", func(t *testing.T) {
		rapid.Check(t, func(rt *rapid.T) {
			doc := rapid.SliceOfN(rapid.Byte(), 0, propDocMax).Draw(rt, "doc")
			s := &SigSketch{Sig: MinHash(doc, propMinHashOptions()), Created: propCreated(rt)}
			propRequireStableEncoding(rt, s, func() Sketch { return &SigSketch{} })

			// The compact form has no QPKS frame around it and is what actually travels in a
			// store record, so it gets the same identity stated separately.
			compact, err := s.Sig.MarshalBinary()
			require.NoError(rt, err)
			var round Signature
			require.NoError(rt, round.UnmarshalBinary(compact))
			again, err := round.MarshalBinary()
			require.NoError(rt, err)
			require.True(rt, bytes.Equal(compact, again),
				"the compact signature form must re-encode byte-identically")
		})
	})
}

// propCreated draws a construction stamp. It is drawn rather than fixed so that the Created field
// is actually exercised by the round trip; core.UnixMilli is an int64 and the encoder writes all 64
// bits, so negative values (a pre-epoch stamp) are legal input and must survive.
func propCreated(rt *rapid.T) core.UnixMilli {
	return core.UnixMilli(rapid.Int64().Draw(rt, "created"))
}

// propMinHashOptions returns the Appendix C option set every MinHash property draws against.
func propMinHashOptions() MinHashOptions {
	return MinHashOptions{
		Enabled:      true,
		Permutations: propMinHashPerms,
		ShingleSize:  propMinHashShingle,
	}
}

// propRequireStableEncoding asserts marshal ∘ unmarshal ∘ marshal == marshal for one sketch, using
// fresh to build the intermediate receiver. The intermediate is a FRESH instance rather than the
// original precisely so that a decoder which left a field untouched — inheriting it from whatever
// the receiver already held — is caught here rather than passing because the receiver happened to
// hold the right value already.
func propRequireStableEncoding(rt *rapid.T, s Sketch, fresh func() Sketch) {
	first, err := s.MarshalBinary()
	require.NoError(rt, err)
	require.NotEmpty(rt, first, "MarshalBinary must produce a non-empty frame")

	into := fresh()
	require.NoError(rt, into.UnmarshalBinary(first))
	second, err := into.MarshalBinary()
	require.NoError(rt, err)
	require.True(rt, bytes.Equal(first, second),
		"marshal ∘ unmarshal ∘ marshal must be byte-identical to marshal (%d vs %d bytes)",
		len(first), len(second))
	require.Equal(rt, s.Header(), into.Header(),
		"the decoded sketch must report the same header as the one that was encoded")
}

// TestProp_UnmarshalNeverPanics is the fuzz surface stated as a property: every decoder in this
// package must answer an arbitrary buffer with a sentinel error or with a usable sketch, and never
// with a panic. A daemon that dies on a corrupt sketch file takes observability down with it
// (§12.3), and the file is attacker-shaped only in the weak sense — bit rot and interrupted writes
// produce exactly the same inputs.
//
// Half the draws are free-form bytes and half are MUTATIONS of a valid frame. Free-form bytes are
// rejected at the magic check almost every time, which exercises one branch and nothing behind it;
// a mutated frame reaches the param walk, the length arithmetic and the body decoder, which is
// where the allocation-sizing hazards actually live.
func TestProp_UnmarshalNeverPanics(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		data := propFuzzInput(rt)

		for _, tc := range propDecoders() {
			s := tc.fresh()
			err := s.UnmarshalBinary(data)
			if err != nil {
				propRequireSentinel(rt, err, tc.name)
				continue
			}
			// The survivor must re-marshal cleanly: a frame this package accepted but could not
			// write back is a sketch that cannot survive the next restart, which is the one
			// failure §6.2 exists to prevent.
			frame, marshalErr := s.MarshalBinary()
			require.NoError(rt, marshalErr, "%s accepted a frame it cannot re-encode", tc.name)
			require.NotEmpty(rt, frame, "%s re-encoded to nothing", tc.name)
		}

		var sig Signature
		if err := sig.UnmarshalBinary(data); err != nil {
			propRequireSentinel(rt, err, "Signature")
		} else {
			compact, marshalErr := sig.MarshalBinary()
			require.NoError(rt, marshalErr, "Signature accepted a buffer it cannot re-encode")
			require.True(rt, bytes.Equal(data, compact),
				"the compact signature form is fully determined by Perms and Mins, so a decoded "+
					"signature must re-encode to the exact bytes it came from")
		}
	})
}

// propDecoder pairs a sketch constructor with the name to report it under.
type propDecoder struct {
	name  string
	fresh func() Sketch
}

// propDecoders returns one entry per framed sketch type. The constructors are the smallest legal
// ones: a decoder that ignored a declared dimension and reused the receiver's would then produce a
// visibly wrong answer rather than accidentally the right one.
func propDecoders() []propDecoder {
	return []propDecoder{
		{"Bloom", func() Sketch { return NewBloom(1, propBloomFPRate) }},
		{"CMS", func() Sketch { return NewCMS(propCMSEpsilon, propCMSDelta) }},
		{"HLL", func() Sketch { return NewHLL(MinHLLRegisters) }},
		{"MisraGries", func() Sketch { return NewMisraGries(1) }},
		{"SigSketch", func() Sketch { return &SigSketch{} }},
	}
}

// propRequireSentinel asserts err is one of this package's declared sentinels. Every rejection path
// in every decoder here returns one of the nine bare, so a decoder answering anything else is
// reporting a failure no caller has been told how to match on.
func propRequireSentinel(rt *rapid.T, err error, who string) {
	require.True(rt, isSketchSentinel(err),
		"%s rejected a frame with an error outside the package's sentinel set: %v", who, err)
}

// propFuzzInput draws the decoder input: either free-form bytes or a valid frame with a handful of
// bytes overwritten. See TestProp_UnmarshalNeverPanics for why both shapes are needed.
func propFuzzInput(rt *rapid.T) []byte {
	if rapid.Bool().Draw(rt, "mutated") {
		return propMutatedFrame(rt)
	}
	return rapid.SliceOfN(rapid.Byte(), 0, propBodyMax).Draw(rt, "raw")
}

// propMutatedFrame draws one of the five real frames and overwrites 1–8 of its bytes, then repairs
// the checksum on half the draws.
//
// The repair is what reaches the interesting half of the decoders. Without it every mutation dies at
// the CRC — the last check of DecodeHeader's pass 1 — and the param walk, the allocation sizing and
// the five body decoders behind it are never entered, which is the whole surface the ceilings in
// errors.go exist to defend. The unrepaired half is still drawn, because a broken checksum is
// exactly what bit rot looks like and ErrCorrupt has to stay reachable.
func propMutatedFrame(rt *rapid.T) []byte {
	frame := rapid.SampledFrom(propReferenceFrames()).Draw(rt, "frame")
	out := append([]byte(nil), frame...)
	edits := rapid.IntRange(1, 8).Draw(rt, "edits")
	for i := 0; i < edits; i++ {
		at := rapid.IntRange(0, len(out)-1).Draw(rt, "at")
		out[at] = rapid.Byte().Draw(rt, "byte")
	}
	if rapid.Bool().Draw(rt, "repaired") {
		return fuzzFixCRC(out)
	}
	return out
}

// propReferenceFrames returns one small, valid frame per sketch kind. They are built once per
// process rather than per draw: rapid draws thousands of inputs and re-marshalling a Count-Min
// table for each would make this property cost more than every other one combined.
func propReferenceFrames() [][]byte {
	propFramesOnce.Do(func() {
		build := []func() Sketch{
			func() Sketch {
				b := NewBloom(1, propBloomFPRate)
				b.Add([]byte("seed"))
				return b
			},
			func() Sketch {
				c := NewCMS(maxCMSEpsilon, maxCMSDelta)
				c.Add([]byte("seed"), 1)
				return c
			},
			func() Sketch {
				h := NewHLL(MinHLLRegisters)
				h.Add([]byte("seed"))
				return h
			},
			func() Sketch {
				m := NewMisraGries(2)
				m.Add("seed", 1)
				return m
			},
			func() Sketch {
				return &SigSketch{Sig: MinHash([]byte("a reference document, long enough to shingle"), propMinHashOptions())}
			},
		}
		for _, f := range build {
			frame, err := f().MarshalBinary()
			if err != nil {
				panic("property fixture: reference frame failed to marshal: " + err.Error())
			}
			propFrames = append(propFrames, frame)
		}
	})
	return propFrames
}

// propFrames caches propReferenceFrames' output; propFramesOnce guards it. A plain package-level
// var would be built at init time even for a `go test -run TestHeader` invocation that never needs
// it.
var (
	propFrames     [][]byte
	propFramesOnce sync.Once
)

// TestProp_MinHashJaccardAccuracy is §15's "MinHash Jaccard within 0.1 of exact on known sets"
// stated over drawn documents rather than one fixture, at the tolerance the number of draws
// demands.
//
// The tolerance is 0.20 and MUST NOT be tightened. The estimator is a mean of Perms independent
// Bernoulli(J) samples, so its worst-case standard error at P = 128 is √(0.25/128) = 0.0442, and
// 0.20 is 4.5 σ. The obvious-looking 3 σ bound (0.15) is wrong for a PROPERTY test: it leaves a
// per-draw tail of roughly 0.3 %, and rapid runs 100 draws per invocation, so the compounded
// probability that the suite fails on a correct implementation is about 26 %. A bound is chosen
// from the number of draws, not from one draw. §15's 0.1 is a statement about a fixed known set,
// which minhash_test.go's TestMinHash_OneNewFailure and the conformance suite both assert directly.
//
// The two documents share a drawn prefix and differ in a drawn tail, which sweeps the true Jaccard
// across its whole range; drawing both freely would put almost every pair near 0 and the property
// would only ever test one end of the estimator. Both stay under 4 KiB, below MinHashSampleTarget's
// threshold, so the content-defined sampler keeps every shingle and the standard error above is the
// one that actually applies.
func TestProp_MinHashJaccardAccuracy(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		shared := rapid.SliceOfN(rapid.Byte(), 0, propDocMax).Draw(rt, "shared")
		tailA := rapid.SliceOfN(rapid.Byte(), propDocMin, propDocMax).Draw(rt, "tailA")
		tailB := rapid.SliceOfN(rapid.Byte(), propDocMin, propDocMax).Draw(rt, "tailB")

		docA := append(append([]byte(nil), shared...), tailA...)
		docB := append(append([]byte(nil), shared...), tailB...)

		opts := propMinHashOptions()
		got := MinHash(docA, opts).Jaccard(MinHash(docB, opts))
		exact := mhExactJaccard(docA, docB, propMinHashShingle)

		require.InDelta(rt, exact, got, propJaccardTolerance,
			"MinHash estimated Jaccard %v against an exact shingle Jaccard of %v "+
				"(|A| = %d bytes, |B| = %d bytes); the bound is 4.5 standard errors at P = %d",
			got, exact, len(docA), len(docB), propMinHashPerms)
	})
}

// TestProp_MinHashSelfSimilarity pins the two degenerate comparisons every consumer relies on: a
// signature is a perfect match for itself, and IsNearDup agrees even at the strictest possible
// threshold. It holds for the empty-document signature too — P copies of math.MaxUint64 — because
// two empty shingle sets ARE identical; that case is deliberately in scope, since a store record
// for a zero-length tool result would otherwise report itself as not a duplicate of itself.
func TestProp_MinHashSelfSimilarity(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		doc := rapid.SliceOfN(rapid.Byte(), 0, propDocMax).Draw(rt, "doc")
		perms := rapid.IntRange(MinPermutations, MaxPermutations).Draw(rt, "perms")
		shingle := rapid.IntRange(minShingleSize, maxShingleSize).Draw(rt, "shingle")

		s := MinHash(doc, MinHashOptions{Enabled: true, Permutations: perms, ShingleSize: shingle})

		require.Equal(rt, 1.0, s.Jaccard(s),
			"a signature must be a perfect Jaccard match for itself (%d bytes, %d perms)",
			len(doc), perms)
		require.True(rt, s.IsNearDup(s, 1.0),
			"a signature must be a near-duplicate of itself at threshold 1.0")
	})
}

// TestProp_HeaderRoundTrip is the QPKS container's own round-trip law: whatever EncodeHeader
// accepts, DecodeHeader must return unchanged, with the CRC32C it reports equal to the four bytes
// actually on the wire. Everything else in this package is a body inside that frame, so a header
// that lost a param or mis-measured a body would corrupt all five sketches at once.
//
// Two fields are asserted against what the ENCODER guarantees rather than against the input, and
// both are documented behaviour rather than slack in the test. Magic is always HeaderMagic, so no
// caller can emit a frame this package would then refuse to read. A zero-param header decodes to a
// nil Params map, because len(nil) and len(map{}) both encode to ParamCount == 0 and nil is the
// value that makes the far more common literal — a Header written with no Params field at all —
// round-trip exactly (header.go, "Params normalization on a zero-param frame").
//
// Param values are drawn finite. math.Float64bits round-trips a NaN bit pattern exactly, but
// require.Equal reports two NaNs as unequal, so a drawn NaN would fail a correct encoder.
func TestProp_HeaderRoundTrip(t *testing.T) {
	kinds := []Kind{KindBloom, KindCMS, KindHLL, KindMisraGries, KindMinHash}

	rapid.Check(t, func(rt *rapid.T) {
		names := rapid.SliceOfNDistinct(
			rapid.StringMatching(`[a-z0-9.]{1,32}`), 0, propParamMax, rapid.ID,
		).Draw(rt, "names")

		params := make(map[string]float64, len(names))
		for _, n := range names {
			params[n] = rapid.Float64Range(-propParamValueBound, propParamValueBound).
				Draw(rt, "value/"+n)
		}

		in := Header{
			Ver:     FormatVersion,
			Kind:    rapid.SampledFrom(kinds).Draw(rt, "kind"),
			Params:  params,
			Count:   rapid.Uint64().Draw(rt, "count"),
			Created: propCreated(rt),
		}
		body := rapid.SliceOfN(rapid.Byte(), 0, propBodyMax).Draw(rt, "body")

		frame, err := EncodeHeader(in, body)
		require.NoError(rt, err)
		require.GreaterOrEqual(rt, len(frame), minFrameLen)

		got, gotBody, err := DecodeHeader(frame)
		require.NoError(rt, err)

		require.Equal(rt, HeaderMagic, got.Magic, "the encoder must stamp QPKS regardless of input")
		require.Equal(rt, in.Ver, got.Ver)
		require.Equal(rt, in.Kind, got.Kind)
		require.Equal(rt, in.Count, got.Count)
		require.Equal(rt, in.Created, got.Created)
		if len(params) == 0 {
			require.Nil(rt, got.Params, "a zero-param frame must decode to a nil Params map")
		} else {
			require.Equal(rt, params, got.Params)
		}
		require.True(rt, bytes.Equal(body, gotBody), "the decoded body must equal the encoded one")
		require.Equal(rt, binary.LittleEndian.Uint32(frame[len(frame)-crcLen:]), got.CRC32C,
			"the reported CRC32C must be the four bytes actually on the wire")
	})
}

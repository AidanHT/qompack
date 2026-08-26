package scheduler

import (
	"encoding/binary"
	"hash/crc32"
	"math"
	"testing"
	"time"

	"github.com/qompack/qompack/internal/core"
	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"
)

// This file holds the plan's fifteen bocd_test.go cases (plans/V4-SP-12-scheduler-l3.md,
// "internal/scheduler/bocd_test.go") plus the stepSeries fixture the "Shared fixtures" table
// assigns here, two extra cases that pin the binary layout and the hard-cap behaviour, and the two
// benchmarks. It is a _test.go file, so the nomagic literal sets do not apply.

// ── Series fixtures ─────────────────────────────────────────────────────────────────────────

const (
	// seriesSeed is the fixed LCG seed every series draws from; determinism across runs is a
	// tested property (TestBOCD_Deterministic), so nothing here may touch math/rand.
	seriesSeed uint32 = 0x5EED_1234
	// seriesNoise is the half-width of the uniform noise around each series mean. Uniform noise
	// has no tails, so a stationary stretch can never produce the multi-sigma outlier that would
	// make a false changepoint a matter of luck.
	seriesNoise = 0.05
	// seriesBaseMean is the mean stepSeries starts from.
	seriesBaseMean = 0.2
)

// lcg is a 32-bit linear congruential generator (Numerical Recipes constants). It is deliberately
// not math/rand: the fixtures must be bit-identical on every platform and Go version.
type lcg struct{ state uint32 }

// next returns the next draw in [0, 1).
func (g *lcg) next() float64 {
	g.state = g.state*1664525 + 1013904223
	return float64(g.state>>8) / float64(1<<24)
}

// featuresAt builds a Features whose every mapped stream (paths, tools, lexical, time, todos)
// equals v under featureVector's orientation: PathJaccard and LexicalCohesion are inverted, and
// GapSeconds is the inverse of the log1p compression so that the time stream lands on v too.
func featuresAt(v float64) Features {
	return Features{
		PathJaccard:     1 - v,
		ToolShift:       v,
		LexicalCohesion: 1 - v,
		GapSeconds:      math.Expm1(v * math.Log1p(gapReferenceSeconds)),
		TodoTransition:  v,
	}
}

// noisySeries draws n observations around meanAt(i) with uniform ±seriesNoise noise, clamped to
// [0,1], from the fixed seed.
func noisySeries(n int, meanAt func(i int) float64) []Features {
	g := &lcg{state: seriesSeed}
	out := make([]Features, n)
	for i := range out {
		v := meanAt(i) + (g.next()-0.5)*2*seriesNoise
		out[i] = featuresAt(clamp01(v))
	}
	return out
}

// stepSeries is the "Shared fixtures" table's series: n observations drawn deterministically
// (fixed LCG seed) from mean 0.2 then, after n/2, mean 0.2+shift.
func stepSeries(n int, shift float64) []Features {
	return noisySeries(n, func(i int) float64 {
		if i < n/2 {
			return seriesBaseMean
		}
		return seriesBaseMean + shift
	})
}

// piecewiseSeries is a piecewise-stationary series of n observations: segments of length 16..96
// (LCG-drawn) alternating between means 0.2 and 0.8. Every segment boundary is a clear step, so
// the posterior collapses and regrows within each segment — the regime TestBOCD_PosteriorBounded
// measures its mean posterior length over.
func piecewiseSeries(n int) []Features {
	g := &lcg{state: seriesSeed ^ 0xA5A5_A5A5}
	means := make([]float64, 0, n)
	for lo := false; len(means) < n; lo = !lo {
		segLen := 16 + int(g.next()*81)
		m := 0.8
		if lo {
			m = 0.2
		}
		for i := 0; i < segLen && len(means) < n; i++ {
			means = append(means, m)
		}
	}
	return noisySeries(n, func(i int) float64 { return means[i] })
}

// ── Helpers ─────────────────────────────────────────────────────────────────────────────────

// newTestDetector builds the Appendix C detector: hazard 0.004 over paths/tools/time/todos.
func newTestDetector() Detector {
	cp := baseCfg().Changepoint
	return NewBOCD(cp.HazardRate, cp.Features)
}

// feed observes every element of xs in order and returns the state after each.
func feed(d Detector, xs []Features) []ChangepointState {
	out := make([]ChangepointState, len(xs))
	for i, f := range xs {
		out[i] = d.Observe(f)
	}
	return out
}

// declaredIndices lists the indices at which AtChangepoint was true.
func declaredIndices(states []ChangepointState) []int {
	var out []int
	for i, s := range states {
		if s.AtChangepoint {
			out = append(out, i)
		}
	}
	return out
}

// requireNormalized asserts the plan's invariant: |Σ Posterior − 1| < 1e-9, every entry ≥ 0,
// nothing NaN, and the scalar fields finite. The per-entry scan is a plain loop with one
// assertion at the end: every require call unwinds the stack for t.Helper, and the property tests
// check millions of entries.
func requireNormalized(t require.TestingT, s ChangepointState) {
	require.NotEmpty(t, s.Posterior)
	sum, bad := 0.0, -1
	for i, v := range s.Posterior {
		if math.IsNaN(v) || v < 0 {
			bad = i
			break
		}
		sum += v
	}
	require.Equal(t, -1, bad, "Posterior[%d] = %v is not a probability", bad, s.Posterior[max(bad, 0)])
	require.InDelta(t, 1.0, sum, 1e-9, "posterior mass")
	require.False(t, math.IsNaN(s.ProbChangepoint) || math.IsInf(s.ProbChangepoint, 0), "ProbChangepoint not finite")
	require.GreaterOrEqual(t, s.RunLength, 0)
	require.Less(t, s.RunLength, len(s.Posterior))
}

// marshalValid marshals d and fails the test on error.
func marshalValid(t *testing.T, d Detector) []byte {
	t.Helper()
	buf, err := d.MarshalBinary()
	require.NoError(t, err)
	return buf
}

// The spec's byte layout, restated here independently of bocd.go's constants so the tests pin the
// format rather than the implementation's own view of it.
const (
	layoutOffVersion      = 4
	layoutOffFeatureCount = 6
	layoutOffHazard       = 8
	layoutOffRunCount     = 16
	layoutOffSince        = 20
	layoutOffDeclared     = 24
	layoutOffReserved     = 25
	layoutOffNames        = 28
	layoutCRCLen          = 4
)

// withCRC recomputes the trailing CRC32C so a header mutation fails at its own check, not at the
// checksum.
func withCRC(buf []byte) []byte {
	out := append([]byte(nil), buf...)
	body := out[:len(out)-layoutCRCLen]
	binary.LittleEndian.PutUint32(out[len(out)-layoutCRCLen:], crc32.Checksum(body, crc32.MakeTable(crc32.Castagnoli)))
	return out
}

// featureGen draws a Features whose fields range well outside [0,1] and include the IEEE special
// values, so the property tests cover everything featureVector must clamp.
func featureGen() *rapid.Generator[Features] {
	field := rapid.OneOf(
		rapid.Float64Range(-2, 3),
		rapid.SampledFrom([]float64{math.NaN(), math.Inf(1), math.Inf(-1), 0, 1, 1e300, -1e300}),
	)
	return rapid.Custom(func(rt *rapid.T) Features {
		return Features{
			PathJaccard:     field.Draw(rt, "paths"),
			ToolShift:       field.Draw(rt, "tools"),
			LexicalCohesion: field.Draw(rt, "lexical"),
			GapSeconds:      field.Draw(rt, "gap"),
			TodoTransition:  field.Draw(rt, "todos"),
		}
	})
}

// ── The plan's fifteen cases ────────────────────────────────────────────────────────────────

func TestBOCD_StationarySeries_RunLengthGrows(t *testing.T) {
	d := newTestDetector()
	states := feed(d, stepSeries(200, 0))
	last := states[len(states)-1]
	require.GreaterOrEqual(t, last.RunLength, 100, "run length must keep growing on a stationary series")
	require.LessOrEqual(t, len(declaredIndices(states)), 2, "declarations on a stationary series: %v", declaredIndices(states))
	for _, s := range states {
		requireNormalized(t, s)
	}
	require.Equal(t, last, d.State(), "State() must mirror the last Observe")
}

func TestBOCD_StepChange_DetectedWithinFiveObservations(t *testing.T) {
	d := newTestDetector()
	states := feed(d, stepSeries(200, 0.6))
	decl := declaredIndices(states)
	require.NotEmpty(t, decl, "a 0.6 step must be declared")
	require.GreaterOrEqual(t, decl[0], 100, "first declaration before the step: %v", decl)
	require.LessOrEqual(t, decl[0], 105, "first declaration too late: %v", decl)
	require.Greater(t, states[decl[0]].ProbChangepoint, bocdChangepointThreshold)
}

func TestBOCD_Hysteresis_NoStorm(t *testing.T) {
	// 60 observations at 0.2, then a 40-observation plateau at 0.9 following the shift.
	series := noisySeries(100, func(i int) float64 {
		if i < 60 {
			return seriesBaseMean
		}
		return 0.9
	})
	d := newTestDetector()
	states := feed(d, series)
	decl := declaredIndices(states)
	require.NotEmpty(t, decl, "the shift onto the plateau must be declared")
	for start := 0; start+bocdMinTurnsBetweenDecls <= len(states); start++ {
		n := 0
		for i := start; i < start+bocdMinTurnsBetweenDecls; i++ {
			if states[i].AtChangepoint {
				n++
			}
		}
		require.LessOrEqual(t, n, 1, "window [%d,%d) holds %d declarations: %v", start, start+bocdMinTurnsBetweenDecls, n, decl)
	}
}

func TestBOCD_PosteriorNormalized_Property(t *testing.T) {
	gen := rapid.SliceOfN(featureGen(), 500, 500)
	rapid.Check(t, func(rt *rapid.T) {
		d := newTestDetector()
		for i, f := range gen.Draw(rt, "obs") {
			s := d.Observe(f)
			requireNormalized(rt, s)
			require.LessOrEqual(rt, len(s.Posterior), bocdMaxRunLength, "index %d", i)
		}
	})
}

func TestBOCD_PosteriorBounded(t *testing.T) {
	const (
		total = 5000
		batch = 500
		// growthTolerance: the plan's clause is "per-op wall time non-increasing between the
		// first and last 500 observations". Per-op work is Θ(len(posterior)), so the assertion
		// is made on that deterministic proxy — Σ posterior length over the last batch ≤ 3 × the
		// first batch — and wall time is only logged: a genuine O(t) growth shows a ratio near 9,
		// while a clock ratio flakes under co-load (ruling R40).
		growthTolerance = 3
	)
	d := newTestDetector()
	series := piecewiseSeries(total)
	sumLen, maxLen := 0, 0
	firstLen, lastLen := 0, 0
	var firstBatch, lastBatch time.Duration
	start := time.Now()
	for i, f := range series {
		switch i {
		case batch:
			firstBatch = time.Since(start)
		case total - batch:
			start = time.Now()
		}
		s := d.Observe(f)
		n := len(s.Posterior)
		require.LessOrEqual(t, n, bocdMaxRunLength, "index %d", i)
		require.Equal(t, n, len(d.State().Posterior))
		sumLen += n
		maxLen = max(maxLen, n)
		switch {
		case i < batch:
			firstLen += n
		case i >= total-batch:
			lastLen += n
		}
	}
	lastBatch = time.Since(start)
	mean := float64(sumLen) / float64(total)
	t.Logf("posterior length: mean %.1f, max %d; Σlen first batch %d, last batch %d; wall first %v, last %v",
		mean, maxLen, firstLen, lastLen, firstBatch, lastBatch)
	require.Less(t, mean, 64.0, "mean posterior length")
	require.LessOrEqual(t, lastLen, growthTolerance*firstLen,
		"per-op work (Σ posterior length) grew from %d to %d per %d observations", firstLen, lastLen, batch)
}

func TestBOCD_MarshalRoundTrip_Property(t *testing.T) {
	gen := featureGen()
	rapid.Check(t, func(rt *rapid.T) {
		k := rapid.IntRange(0, 300).Draw(rt, "k")
		d := newTestDetector()
		for i := 0; i < k; i++ {
			d.Observe(gen.Draw(rt, "obs"))
		}
		buf, err := d.MarshalBinary()
		require.NoError(rt, err)

		d2 := newTestDetector()
		require.NoError(rt, d2.UnmarshalBinary(buf))
		require.Equal(rt, d.State(), d2.State())

		buf2, err := d2.MarshalBinary()
		require.NoError(rt, err)
		require.Equal(rt, buf, buf2, "re-marshal must be byte-identical")

		// The restored detector must also continue identically.
		f := gen.Draw(rt, "next")
		require.Equal(rt, d.Observe(f), d2.Observe(f))
	})
}

func TestBOCD_UnmarshalRejectsCorruption(t *testing.T) {
	src := newTestDetector()
	feed(src, stepSeries(20, 0))
	valid := marshalValid(t, src)
	require.Equal(t, "paths", string(valid[layoutOffNames+1:layoutOffNames+6]), "fixture assumes the first feature is paths")
	require.Equal(t, "tools", string(valid[layoutOffNames+7:layoutOffNames+12]), "fixture assumes the second feature is tools")

	cases := []struct {
		name   string
		mutate func(b []byte) []byte
		detail string
	}{
		{"short buffer", func(b []byte) []byte { return b[:31] }, "too short"},
		{"bad magic", func(b []byte) []byte { b[0] = 'X'; return withCRC(b) }, "magic"},
		{"version 2", func(b []byte) []byte { binary.LittleEndian.PutUint16(b[layoutOffVersion:], 2); return withCRC(b) }, "version"},
		{"F=0", func(b []byte) []byte { binary.LittleEndian.PutUint16(b[layoutOffFeatureCount:], 0); return withCRC(b) }, "feature count"},
		{"F=9", func(b []byte) []byte { binary.LittleEndian.PutUint16(b[layoutOffFeatureCount:], 9); return withCRC(b) }, "feature count"},
		{"R=0", func(b []byte) []byte { binary.LittleEndian.PutUint32(b[layoutOffRunCount:], 0); return withCRC(b) }, "run count"},
		{"R=513", func(b []byte) []byte { binary.LittleEndian.PutUint32(b[layoutOffRunCount:], 513); return withCRC(b) }, "run count"},
		{"unknown feature name", func(b []byte) []byte { b[layoutOffNames+1] = 'z'; return withCRC(b) }, "unknown feature"},
		{"truncated by 1 byte", func(b []byte) []byte { return b[:len(b)-1] }, "length"},
		{"extended by 1 byte", func(b []byte) []byte { return append(b, 0) }, "length"},
		{"flipped CRC byte", func(b []byte) []byte { b[len(b)-1] ^= 0xFF; return b }, "checksum"},
		{"reserved byte set", func(b []byte) []byte { b[layoutOffReserved+1] = 1; return withCRC(b) }, "reserved"},
		{"declared byte 2", func(b []byte) []byte { b[layoutOffDeclared] = 2; return withCRC(b) }, "declared"},
		{"NaN hazard", func(b []byte) []byte {
			binary.LittleEndian.PutUint64(b[layoutOffHazard:], math.Float64bits(math.NaN()))
			return withCRC(b)
		}, "hazard"},
		// Two tampered-but-well-formed payloads (R1#2): every name known but one repeated, so a
		// stream would be counted twice; and a posterior whose mass is not 1, so every weight
		// would run at the wrong scale. The constructor collapses the first and Observe never
		// writes the second; UnmarshalBinary must refuse both.
		{"duplicate feature name", func(b []byte) []byte {
			copy(b[layoutOffNames+7:layoutOffNames+12], "paths") // the second name, "tools", becomes a second "paths"
			return withCRC(b)
		}, "duplicate feature"},
		{"unnormalised posterior", func(b []byte) []byte {
			off := posteriorOffset(b)
			v := math.Float64frombits(binary.LittleEndian.Uint64(b[off:]))
			binary.LittleEndian.PutUint64(b[off:], math.Float64bits(v+0.5))
			return withCRC(b)
		}, "not normalised"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := newTestDetector()
			feed(d, stepSeries(6, 0.5))
			before, beforeBytes := d.State(), marshalValid(t, d)

			err := d.UnmarshalBinary(tc.mutate(append([]byte(nil), valid...)))
			require.Error(t, err)
			require.ErrorIs(t, err, core.ErrNotFound)
			require.ErrorContains(t, err, "scheduler: corrupt BOCD state")
			require.ErrorContains(t, err, tc.detail)
			require.Equal(t, before, d.State(), "receiver state changed")
			require.Equal(t, beforeBytes, marshalValid(t, d), "receiver internals changed")
		})
	}

	// The unmutated buffer still loads, proving the fixture itself was valid.
	require.NoError(t, newTestDetector().UnmarshalBinary(valid))
}

// posteriorOffset is where the posterior starts in a MarshalBinary buffer: right after the
// feature table, whose entries are a length byte followed by that many name bytes.
func posteriorOffset(b []byte) int {
	nf := int(binary.LittleEndian.Uint16(b[layoutOffFeatureCount:]))
	off := layoutOffNames
	for range nf {
		off += 1 + int(b[off])
	}
	return off
}

func TestBOCD_UnmarshalBoundsCheckedBeforeAllocation(t *testing.T) {
	buf := make([]byte, 40)
	copy(buf, "QPKB")
	binary.LittleEndian.PutUint16(buf[layoutOffVersion:], 1)
	binary.LittleEndian.PutUint16(buf[layoutOffFeatureCount:], 1)
	binary.LittleEndian.PutUint64(buf[layoutOffHazard:], math.Float64bits(0.004))
	binary.LittleEndian.PutUint32(buf[layoutOffRunCount:], 4_000_000_000)

	d := newTestDetector()
	before := d.State()
	var err error
	allocs := testing.AllocsPerRun(100, func() { err = d.UnmarshalBinary(buf) })
	require.ErrorIs(t, err, core.ErrNotFound)
	require.ErrorContains(t, err, "run count")
	require.Less(t, allocs, 64.0, "rejecting path must not allocate for the claimed R")
	require.Equal(t, before, d.State())
}

func TestBOCD_Deterministic(t *testing.T) {
	series := noisySeries(300, func(i int) float64 {
		switch {
		case i < 100:
			return 0.2
		case i < 180:
			return 0.7
		default:
			return 0.35
		}
	})
	a, b := newTestDetector(), newTestDetector()
	for i, f := range series {
		sa, sb := a.Observe(f), b.Observe(f)
		require.Equal(t, sa, sb, "index %d", i)
		require.Equal(t, a.State(), b.State(), "index %d", i)
	}
	require.Equal(t, marshalValid(t, a), marshalValid(t, b))
}

func TestBOCD_FeatureSubsetHonoured(t *testing.T) {
	d := NewBOCD(baseCfg().Changepoint.HazardRate, []string{"time"})
	buf := marshalValid(t, d)
	require.Equal(t, uint16(1), binary.LittleEndian.Uint16(buf[layoutOffFeatureCount:]), "featureCount")
	require.Equal(t, "time", string(buf[layoutOffNames+1:layoutOffNames+5]))

	g := &lcg{state: seriesSeed}
	var states []ChangepointState
	for i := 0; i < 50; i++ {
		jaccard := float64(i % 2) // hard alternation
		if i%5 == 0 {
			jaccard = g.next()
		}
		states = append(states, d.Observe(Features{PathJaccard: jaccard, GapSeconds: 30}))
	}
	require.Empty(t, declaredIndices(states), "an unselected stream must never declare")
	for _, s := range states {
		requireNormalized(t, s)
	}
}

func TestBOCD_UnknownFeaturesDropped(t *testing.T) {
	d, ok := NewBOCD(baseCfg().Changepoint.HazardRate, []string{"paths", "bogus"}).(*bocd)
	require.True(t, ok)
	require.Equal(t, []string{"paths"}, d.features)
}

func TestBOCD_EmptyFeaturesFallsBackToDefaults(t *testing.T) {
	d, ok := NewBOCD(baseCfg().Changepoint.HazardRate, nil).(*bocd)
	require.True(t, ok)
	require.Equal(t, []string{"paths", "tools", "time", "todos"}, d.features)

	d2, ok := NewBOCD(baseCfg().Changepoint.HazardRate, []string{"bogus", ""}).(*bocd)
	require.True(t, ok)
	require.Equal(t, []string{"paths", "tools", "time", "todos"}, d2.features, "all-unknown also falls back")
}

func TestBOCD_InvalidHazardClamped(t *testing.T) {
	for _, h := range []float64{0, 1, -1, math.NaN(), math.Inf(1), math.Inf(-1)} {
		d := NewBOCD(h, nil)
		b, ok := d.(*bocd)
		require.True(t, ok)
		require.Equal(t, 1.0/float64(bocdMaxRunLength), b.hazard, "hazard %v", h)
		for _, s := range feed(d, stepSeries(50, 0.5)) {
			requireNormalized(t, s)
		}
	}
	// A valid hazard is kept as given.
	b, ok := NewBOCD(0.25, nil).(*bocd)
	require.True(t, ok)
	require.Equal(t, 0.25, b.hazard)
}

func TestBOCD_ResetRestoresPrior(t *testing.T) {
	d := newTestDetector()
	states := feed(d, stepSeries(100, 0.6))
	require.NotEmpty(t, declaredIndices(states), "precondition: the run had a changepoint")
	require.Greater(t, len(d.State().Posterior), 1)

	d.Reset()
	s := d.State()
	require.Len(t, s.Posterior, 1)
	require.Equal(t, 0, s.RunLength)
	require.False(t, s.AtChangepoint)
	require.Equal(t, 1.0, s.ProbChangepoint)
	require.Equal(t, marshalValid(t, newTestDetector()), marshalValid(t, d), "Reset must be indistinguishable from a fresh detector")
}

func TestBOCD_DegeneratePosteriorSelfHeals(t *testing.T) {
	d := newTestDetector()
	feed(d, stepSeries(80, 0))

	// The public path: +Inf gap clamps to 1.0 and must simply be a (large) observation.
	s := d.Observe(Features{GapSeconds: math.Inf(1)})
	requireNormalized(t, s)

	// The degenerate path proper: a NaN vector makes the joint predictive NaN, which the update
	// must answer with Reset rather than a NaN posterior. Reach it through the package-internal
	// seam, since featureVector clamps every public input.
	b, ok := d.(*bocd)
	require.True(t, ok)
	require.NotPanics(t, func() { s = b.observeVector([]float64{math.NaN(), math.NaN(), math.NaN(), math.NaN()}) })
	requireNormalized(t, s)
	require.Equal(t, []float64{1}, s.Posterior, "self-heal restarts the run-length distribution")
	require.Equal(t, 0, s.RunLength)
	require.False(t, s.AtChangepoint)
	require.Equal(t, s, d.State())

	// And it keeps working afterwards.
	for _, s := range feed(d, stepSeries(20, 0)) {
		requireNormalized(t, s)
	}
	require.Len(t, d.State().Posterior, 21)
}

// ── Extra cases ─────────────────────────────────────────────────────────────────────────────

// TestBOCD_MarshalLayout_FreshDetector pins the byte format of the plan's layout table field by
// field on the smallest possible state, so a drift in any offset is caught here rather than by a
// daemon that cannot read its own state/bocd.json after an upgrade.
func TestBOCD_MarshalLayout_FreshDetector(t *testing.T) {
	cp := baseCfg().Changepoint
	buf := marshalValid(t, NewBOCD(cp.HazardRate, cp.Features))

	names := []byte{5, 'p', 'a', 't', 'h', 's', 5, 't', 'o', 'o', 'l', 's', 4, 't', 'i', 'm', 'e', 5, 't', 'o', 'd', 'o', 's'}
	postOff := layoutOffNames + len(names)
	statsOff := postOff + 8
	crcOff := statsOff + 8*4*4
	require.Len(t, buf, crcOff+layoutCRCLen)

	require.Equal(t, "QPKB", string(buf[:4]))
	require.Equal(t, uint16(1), binary.LittleEndian.Uint16(buf[layoutOffVersion:]))
	require.Equal(t, uint16(4), binary.LittleEndian.Uint16(buf[layoutOffFeatureCount:]))
	require.Equal(t, math.Float64bits(cp.HazardRate), binary.LittleEndian.Uint64(buf[layoutOffHazard:]))
	require.Equal(t, uint32(1), binary.LittleEndian.Uint32(buf[layoutOffRunCount:]))
	require.Equal(t, uint32(bocdMinTurnsBetweenDecls), binary.LittleEndian.Uint32(buf[layoutOffSince:]))
	require.Equal(t, byte(0), buf[layoutOffDeclared])
	require.Equal(t, []byte{0, 0, 0}, buf[layoutOffReserved:layoutOffNames])
	require.Equal(t, names, buf[layoutOffNames:postOff])
	require.Equal(t, 1.0, math.Float64frombits(binary.LittleEndian.Uint64(buf[postOff:])))
	for f := 0; f < 4; f++ {
		row := buf[statsOff+f*32:]
		require.Equal(t, bocdPriorMu, math.Float64frombits(binary.LittleEndian.Uint64(row[0:])), "feature %d mu", f)
		require.Equal(t, bocdPriorKappa, math.Float64frombits(binary.LittleEndian.Uint64(row[8:])), "feature %d kappa", f)
		require.Equal(t, bocdPriorAlpha, math.Float64frombits(binary.LittleEndian.Uint64(row[16:])), "feature %d alpha", f)
		require.Equal(t, bocdPriorBeta, math.Float64frombits(binary.LittleEndian.Uint64(row[24:])), "feature %d beta", f)
	}
	want := crc32.Checksum(buf[:crcOff], crc32.MakeTable(crc32.Castagnoli))
	require.Equal(t, want, binary.LittleEndian.Uint32(buf[crcOff:]))
}

// TestBOCD_RowPredictiveMatchesPerFeature pins the folded, per-row predictive that the update
// evaluates against the plan's per-feature Student-t formula computed independently here with
// math.Lgamma on each row's own parameters. The fold is algebra, not approximation, so the two
// agree to floating-point rounding across every row of a mixed run and every probe vector.
func TestBOCD_RowPredictiveMatchesPerFeature(t *testing.T) {
	perFeature := func(x float64, p ngParams) float64 {
		nu := 2 * p.Alpha
		scale2 := p.Beta * (p.Kappa + 1) / (p.Alpha * p.Kappa)
		d := x - p.Mu
		lgNum, _ := math.Lgamma((nu + 1) / 2)
		lgDen, _ := math.Lgamma(nu / 2)
		return lgNum - lgDen - 0.5*math.Log(nu*math.Pi*scale2) - ((nu+1)/2)*math.Log1p(d*d/(nu*scale2))
	}
	d := newTestDetector()
	feed(d, stepSeries(200, 0.6)) // rows straddling the step carry inflated Beta; the rest are tight
	b, ok := d.(*bocd)
	require.True(t, ok)
	require.Greater(t, len(b.post), 20, "precondition: a run with many live rows")

	probes := [][]float64{{0, 0, 0, 0}, {1, 1, 1, 1}, {0.2, 0.8, 0.5, 0.3}, {0.21, 0.19, 0.2, 0.22}}
	for r, row := range b.stats {
		for _, x := range probes {
			want := 0.0
			for i, p := range row {
				want += perFeature(x[i], p)
			}
			got := b.logPredictiveRow(r, x, row)
			require.InDelta(t, want, got, 1e-9*math.Max(1, math.Abs(want)), "row %d probe %v", r, x)
		}
	}
}

// TestBOCD_HardCapConservesMass drives a stationary series past bocdMaxRunLength. The hard cap
// must bound the posterior without losing the run: no declaration, mass conserved, and the run
// length saturating at the cap rather than collapsing to zero and re-declaring every 512 turns.
func TestBOCD_HardCapConservesMass(t *testing.T) {
	d := newTestDetector()
	states := feed(d, stepSeries(1400, 0))
	require.Empty(t, declaredIndices(states))
	for i, s := range states {
		requireNormalized(t, s)
		require.LessOrEqual(t, len(s.Posterior), bocdMaxRunLength, "index %d", i)
	}
	last := states[len(states)-1]
	require.Len(t, last.Posterior, bocdMaxRunLength)
	require.Equal(t, bocdMaxRunLength-1, last.RunLength, "run length saturates at the cap")
	require.Greater(t, last.Posterior[bocdMaxRunLength-1], 0.5, "the mass stays on the longest run")

	// A step after a capped run is still detected promptly.
	post := feed(d, noisySeries(6, func(int) float64 { return 0.8 }))
	decl := declaredIndices(post)
	require.NotEmpty(t, decl)
	require.LessOrEqual(t, decl[0], 1)
}

// ── Benchmarks ──────────────────────────────────────────────────────────────────────────────

// BenchmarkBOCDObserve_4Features proves §6.6's "O(1) amortized" in wall-clock. Budgets:
// full_posterior ≤ 150 µs/op at a 512-entry posterior; steady_state ≤ 20 µs/op at the pruned
// length a piecewise-stationary session settles into.
func BenchmarkBOCDObserve_4Features(b *testing.B) {
	b.Run("full_posterior", func(b *testing.B) {
		d := newTestDetector()
		series := stepSeries(1200, 0)
		feed(d, series[:700])
		if n := len(d.State().Posterior); n != bocdMaxRunLength {
			b.Fatalf("posterior length %d, want %d", n, bocdMaxRunLength)
		}
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			d.Observe(series[700+i%500])
		}
	})
	b.Run("steady_state", func(b *testing.B) {
		d := newTestDetector()
		series := piecewiseSeries(5000)
		feed(d, series[:1000])
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			d.Observe(series[1000+i%4000])
		}
	})
}

// BenchmarkBOCDMarshal bounds the idle-tick persist cost: ≤ 2 ms/op at 512 entries × 4 features.
func BenchmarkBOCDMarshal(b *testing.B) {
	d := newTestDetector()
	feed(d, stepSeries(1200, 0)[:700])
	if n := len(d.State().Posterior); n != bocdMaxRunLength {
		b.Fatalf("posterior length %d, want %d", n, bocdMaxRunLength)
	}
	buf, err := d.MarshalBinary()
	if err != nil {
		b.Fatal(err)
	}
	b.Logf("serialized size at %d×%d: %d bytes", bocdMaxRunLength, len(baseCfg().Changepoint.Features), len(buf))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := d.MarshalBinary(); err != nil {
			b.Fatal(err)
		}
	}
}

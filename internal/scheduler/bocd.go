package scheduler

import (
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"math"
	"slices"

	"github.com/qompack/qompack/internal/core"
)

// This file is the Detector of Qompack.md §6.6: Adams & MacKay (2007) Bayesian online changepoint
// detection over a run-length posterior with a constant hazard H = hazardRate, one
// Normal-Inverse-Gamma conjugate observation model per feature, and features assumed independent
// so the joint predictive is the product of per-feature Student-t densities.
//
// Why Normal-Inverse-Gamma: it is conjugate to a Normal with unknown mean and variance, so the
// posterior predictive is a closed-form Student-t and every update is four arithmetic operations
// per (row, feature). Nothing is sampled, so the detector is deterministic and cheap enough for
// the daemon's async B-C path.
//
// Why the lgamma tables: a row that has absorbed r observations has alpha = alpha0 + r/2 exactly,
// so the two gamma terms of the Student-t density (and, with kappa = kappa0 + r, its scale
// normalizer) are a pure function of the row index and are looked up rather than recomputed for
// every (row, feature) pair.
//
// Why pruning: it is what makes §6.6's "O(1) amortized" real. Per-observation cost is
// O(len × F) with len bounded by bocdMaxRunLength unconditionally and, in practice, by the
// mass-bearing prefix that survives bocdPruneEpsilon — it never grows with the session length.

const (
	bocdFormatVersion uint16 = 1

	bocdMaxRunLength         = 512  // hard cap on posterior length
	bocdPruneEpsilon         = 1e-4 // tail entries below this are dropped
	bocdMinPosteriorLen      = 2    // never prune below this
	bocdMinTurnsBetweenDecls = 3    // hysteresis: no changepoint storms on a plateau
	bocdChangepointThreshold = 0.5  // declare when P(the newest observation started a run) exceeds this
	bocdMaxFeatures          = 8    // deserialization bound (§13: no unbounded allocation)
	// bocdMassTolerance is how far from 1 the mass of a persisted posterior may be and still be
	// one MarshalBinary wrote: prune renormalises in float64, so a genuine posterior is within a
	// few hundred ulp (~1e-13) of 1, and anything further is a tampered or truncated payload
	// whose weights would run the stream at the wrong scale.
	bocdMassTolerance = 1e-6

	// Normal-Inverse-Gamma prior. mu0 = 0.5 because every mapped feature is in [0,1] (see
	// featureVector); kappa0 = alpha0 = 1 is the weakly-informative choice. beta0 is NOT 1: the
	// prior must be on the features' own scale (ruling R46). With alpha0 = 1 the fresh run's
	// Student-t scale is √(beta0·(kappa0+1)/(alpha0·kappa0)) = √(2·beta0); at beta0 = 1 that is
	// 1.41 on a [0,1] feature, so no excursion is ever surprising and the data only take over
	// after ~2 000 observations — the detector is deaf for a whole session. beta0 = 0.02 puts
	// the prior scale at 0.2 (within-run noise on the mapped features is σ ≈ 0.05–0.15) and
	// lets the run statistics dominate within ~10 observations. Measured on the synthetic
	// corpus at the replay cadence (one observation per tool-bearing turn): 0 of its 105
	// generator boundaries found at beta0 = 1, 24 at 0.02 (Appendix C hazard). Under the
	// daemon's cadence — one empty observation per Stop on top — the count is unmeasured live
	// and was 3 in replay (ADR 0012, ruling R57).
	bocdPriorMu    = 0.5
	bocdPriorKappa = 1.0
	bocdPriorAlpha = 1.0
	bocdPriorBeta  = 0.02

	// gapReferenceSeconds compresses an unbounded inter-turn gap into [0,1] via
	// log1p(gap)/log1p(ref). 600 s is ten minutes: a gap that long is unambiguously a
	// task boundary and saturates the feature. NewBOCD's signature is fixed by
	// 00-ARCHITECTURE §5.13 and carries no idle config, so this scale lives here.
	gapReferenceSeconds = 600.0
)

// Binary state format (MarshalBinary), byte-for-byte, little-endian throughout:
//
//	offset  size                          field
//	0       4                             magic  'Q','P','K','B'
//	4       2   uint16                    version = 1
//	6       2   uint16                    featureCount F (1..8)
//	8       8   float64 (math.Float64bits) hazardRate
//	16      4   uint32                    runCount R (1..512)
//	20      4   uint32                    since   (turns since last declaration)
//	24      1   uint8                     declared (0|1, mirrors last.AtChangepoint)
//	25      3                             reserved, must be zero
//	28      var F × { uint8 nameLen; nameLen bytes UTF-8 }
//	...     8×R float64                   posterior, R entries
//	...     8×R×F×4 float64               stats, row-major: r outer, f inner, (Mu,Kappa,Alpha,Beta)
//	end−4   4   uint32                    CRC32C (Castagnoli) over bytes [0, end−4)
const (
	bocdMagic       = "QPKB"
	bocdOffVersion  = 4
	bocdOffFeatures = 6
	bocdOffHazard   = 8
	bocdOffRunCount = 16
	bocdOffSince    = 20
	bocdOffDeclared = 24
	bocdOffReserved = 25
	bocdHeaderLen   = 28
	bocdCRCLen      = 4
	bocdMinLen      = bocdHeaderLen + bocdCRCLen // the fixed header plus the trailer
	bocdFloatLen    = 8
	ngParamCount    = 4
	ngRowLen        = ngParamCount * bocdFloatLen // one (r, f) cell of stats
)

// knownFeatures maps the Appendix C `changepoint.features` names onto Features fields.
// "lexical" is supported but not in the Appendix C default list.
var knownFeatures = map[string]struct{}{
	"paths": {}, "tools": {}, "lexical": {}, "time": {}, "todos": {},
}

var defaultFeatures = []string{"paths", "tools", "time", "todos"}

// ngParams is one Normal-Inverse-Gamma posterior: the run's mean estimate Mu with pseudo-count
// Kappa, and the variance's Inverse-Gamma shape Alpha and scale Beta.
type ngParams struct{ Mu, Kappa, Alpha, Beta float64 }

// bocd is the Detector. Row r of post/stats is the hypothesis "the current run consists of exactly
// the r most recent observations": row 0 is the empty run (the fresh prior) and row r+1 is row r
// with the newest observation absorbed.
type bocd struct {
	hazard   float64
	features []string
	post     []float64    // post[r] = P(run length == r), normalized, len >= 1
	stats    [][]ngParams // stats[r][f]
	lgNum    []float64    // lgamma((2·alpha0 + r + 1)/2), precomputed
	lgDen    []float64    // lgamma((2·alpha0 + r)/2),     precomputed
	lgScale  []float64    // ½·log(nu·π·(kappa+1)/(alpha·kappa)) at row r, precomputed
	since    int          // turns since the last declared changepoint
	last     ChangepointState

	// Two backings for post and stats, alternated between observations so an update writes into
	// one while reading the other without allocating (len+1)·F cells per observation. bufIdx is
	// the backing the current post/stats occupy, or -1 after Reset/UnmarshalBinary, whose state
	// lives in its own allocation.
	postBuf  [2][]float64
	statsBuf [2][]ngParams
	rowsBuf  [2][][]ngParams
	bufIdx   int
}

// newBOCD is NewBOCD's body (NewBOCD itself keeps its shipped home in detector.go). An
// out-of-range hazard falls back to 1/bocdMaxRunLength; unknown feature names are dropped and
// duplicates collapsed, because a duplicated stream would count its likelihood twice; an empty
// selection falls back to defaultFeatures.
func newBOCD(hazardRate float64, features []string) *bocd {
	h := hazardRate
	if !(h > 0) || h >= 1 || math.IsNaN(h) {
		h = 1.0 / float64(bocdMaxRunLength) // safe fallback; config.Validate normally prevents this
	}
	sel := make([]string, 0, len(features))
	for _, f := range features {
		if _, ok := knownFeatures[f]; ok && !slices.Contains(sel, f) {
			sel = append(sel, f)
		}
	}
	if len(sel) == 0 {
		sel = append(sel, defaultFeatures...)
	}
	b := &bocd{hazard: h, features: sel}
	b.buildLgammaTables()
	b.Reset()
	return b
}

func (b *bocd) buildLgammaTables() {
	n := bocdMaxRunLength + 2
	b.lgNum = make([]float64, n)
	b.lgDen = make([]float64, n)
	b.lgScale = make([]float64, n)
	for r := 0; r < n; r++ {
		// A row that has absorbed r observations has alpha = alpha0 + r/2 and kappa = kappa0 + r
		// exactly (see ngUpdate).
		kappa := bocdPriorKappa + float64(r)
		alpha := bocdPriorAlpha + float64(r)/2
		nu := 2 * alpha
		b.lgNum[r], _ = math.Lgamma((nu + 1) / 2)
		b.lgDen[r], _ = math.Lgamma(nu / 2)
		b.lgScale[r] = 0.5 * math.Log(nu*math.Pi*(kappa+1)/(alpha*kappa))
	}
}

func (b *bocd) priorRow() []ngParams {
	row := make([]ngParams, len(b.features))
	for i := range row {
		row[i] = ngParams{bocdPriorMu, bocdPriorKappa, bocdPriorAlpha, bocdPriorBeta}
	}
	return row
}

// Reset restores the prior: a single empty-run row with all the mass.
func (b *bocd) Reset() {
	b.post = []float64{1}
	b.stats = [][]ngParams{b.priorRow()}
	b.bufIdx = -1
	b.since = bocdMinTurnsBetweenDecls // allow the first genuine changepoint to fire
	b.last = ChangepointState{
		RunLength: 0, ProbChangepoint: 1, AtChangepoint: false,
		Posterior: []float64{1},
	}
}

// featureVector maps Features onto the selected streams. Every mapped component is in [0,1],
// oriented so that higher means more boundary-like. Orientation does not affect BOCD (it detects
// distribution shift either way) but a single documented convention keeps the priors meaningful.
func (b *bocd) featureVector(f Features) []float64 {
	out := make([]float64, len(b.features))
	for i, name := range b.features {
		switch name {
		case "paths":
			out[i] = clamp01(1 - f.PathJaccard) // path NOVELTY
		case "tools":
			out[i] = clamp01(f.ToolShift) // total-variation shift, already 0..1
		case "lexical":
			out[i] = clamp01(1 - f.LexicalCohesion)
		case "time":
			g := f.GapSeconds
			if g < 0 || math.IsNaN(g) {
				g = 0
			}
			out[i] = clamp01(math.Log1p(g) / math.Log1p(gapReferenceSeconds))
		case "todos":
			out[i] = clamp01(f.TodoTransition)
		}
	}
	return out
}

func clamp01(x float64) float64 {
	if math.IsNaN(x) || x < 0 {
		return 0
	}
	if x > 1 {
		return 1
	}
	return x
}

// logPredictiveRow is Σ_f log p(x_f | row_f) for one run-length hypothesis: the product over the
// independent features of the Student-t posterior predictive of a Normal-Inverse-Gamma model,
//
//	nu = 2a, scale² = b(k+1)/(a·k)
//	log p = lgamma((nu+1)/2) − lgamma(nu/2) − ½·log(nu·π·scale²)
//	        − ((nu+1)/2)·log1p((x−mu)²/(nu·scale²))
//
// Every row's k and a are fixed by its index r (k = kappa0 + r, a = alpha0 + r/2, see ngUpdate),
// so the two lgamma terms and ½·log(nu·π·c), c = (k+1)/(a·k), are the lgNum/lgDen/lgScale
// lookups, and the sum over features folds into two logarithms per row: ½·log Π_f b_f and
// ((nu+1)/2)·log Π_f (1 + (x_f−mu_f)²/(nu·c·b_f)).
//
// Why the fold is safe at β0 = 0.02 (ruling R46; the bound below is re-derived for it — at the
// plan's β0 = 1 every factor sat in [1, 1.5]). With x and mu in [0,1], d² ≤ 1; nu·c = 2(k+1)/k
// is 4 at row 0 and falls toward 2 as r grows; and b_f ≥ β0 = 0.02 because ngUpdate only ever
// adds to it. So every factor of the second product lies in [1, 26]: a fresh row reaches
// 1 + 1/(4·0.02) = 13.5, and a zero-variance row at r = 511 (nu·c ≈ 2.004, b still ≈ 0.02)
// reaches 1 + 1/(2.004·0.02) ≈ 26. Over at most bocdMaxFeatures streams the product is ≤ 26⁸
// ≈ 2·10¹¹, and Π_f b_f ≥ 0.02⁸ ≈ 2.6·10⁻¹⁴ (and ≤ (0.02 + 511·0.5)⁸ ≈ 2·10¹⁹, since one update
// adds less than 0.5): neither product overflows or underflows a float64, so both logarithms
// are exact to a few ulp of quantities of order 30 — nothing the 1e-9 pin in
// TestBOCD_RowPredictiveMatchesPerFeature can see — and the row-0 joint, post[0]·exp(·) with
// post[0] ≡ H (see changepointProb) and a bounded exponent, stays positive, so the normaliser
// in observeVector does too. The fold costs two transcendental calls per row instead of two
// per feature.
func (b *bocd) logPredictiveRow(r int, x []float64, row []ngParams) float64 {
	kappa := bocdPriorKappa + float64(r)
	alpha := bocdPriorAlpha + float64(r)/2
	nu := 2 * alpha
	nuc := nu * (kappa + 1) / (alpha * kappa)
	betaProd, shapeProd := 1.0, 1.0
	for i, p := range row {
		d := x[i] - p.Mu
		betaProd *= p.Beta
		shapeProd *= 1 + d*d/(nuc*p.Beta)
	}
	return float64(len(row))*(b.lgNum[r]-b.lgDen[r]-b.lgScale[r]) -
		0.5*math.Log(betaProd) - ((nu+1)/2)*math.Log(shapeProd)
}

// ngUpdate absorbs one observation into a Normal-Inverse-Gamma row.
func ngUpdate(p ngParams, x float64) ngParams {
	k1 := p.Kappa + 1
	d := x - p.Mu
	return ngParams{
		Mu:    (p.Kappa*p.Mu + x) / k1,
		Kappa: k1,
		Alpha: p.Alpha + 0.5,
		Beta:  p.Beta + p.Kappa*d*d/(2*k1),
	}
}

// Observe folds one observation into the run-length posterior. The returned Posterior is a fresh
// copy; callers may retain it.
func (b *bocd) Observe(f Features) ChangepointState {
	return b.observeVector(b.featureVector(f))
}

// freeBuffer returns the index of the backing the current state does not occupy, sized for the
// hard cap on first use.
func (b *bocd) freeBuffer() int {
	idx := 0
	if b.bufIdx == 0 {
		idx = 1
	}
	nf := len(b.features)
	if cap(b.postBuf[idx]) < bocdMaxRunLength+1 {
		b.postBuf[idx] = make([]float64, bocdMaxRunLength+1)
	}
	if cap(b.statsBuf[idx]) < (bocdMaxRunLength+1)*nf {
		b.statsBuf[idx] = make([]ngParams, (bocdMaxRunLength+1)*nf)
	}
	if cap(b.rowsBuf[idx]) < bocdMaxRunLength+1 {
		b.rowsBuf[idx] = make([][]ngParams, bocdMaxRunLength+1)
	}
	return idx
}

// observeVector is Observe on an already-mapped vector (one entry per selected feature). It is
// the seam the degenerate-posterior test drives, since featureVector clamps every public input
// into [0,1] and the joint predictive cannot then underflow. A vector of the wrong width is not an
// observation of this detector's streams and is answered like a degenerate one.
func (b *bocd) observeVector(x []float64) ChangepointState {
	nf := len(x)
	if nf != len(b.features) {
		b.Reset()
		return b.last
	}
	n := len(b.post)
	idx := b.freeBuffer()

	// 1+2. predictive probability of x under each run-length hypothesis, folded straight into the
	//      growth + changepoint messages (Adams & MacKay eq. 1–3)
	grown := b.postBuf[idx][:n+1]
	cp := 0.0
	for r := 0; r < n; r++ {
		joint := b.post[r] * math.Exp(b.logPredictiveRow(r, x, b.stats[r]))
		grown[r+1] = joint * (1 - b.hazard)
		cp += joint * b.hazard
	}
	grown[0] = cp

	// 3. normalize; a degenerate posterior (all-zero / NaN / Inf) is a numerical failure and
	//    the only correct response is to restart the run-length distribution.
	sum := 0.0
	for _, v := range grown {
		sum += v
	}
	if !(sum > 0) || math.IsNaN(sum) || math.IsInf(sum, 0) {
		b.Reset()
		return b.last
	}
	for i := range grown {
		grown[i] /= sum
	}

	// 4. sufficient statistics: row r+1 absorbs x into row r; row 0 is the fresh prior.
	flat := b.statsBuf[idx][:(n+1)*nf]
	rows := b.rowsBuf[idx][:n+1]
	rows[0] = flat[:nf:nf]
	for i := range rows[0] {
		rows[0][i] = ngParams{bocdPriorMu, bocdPriorKappa, bocdPriorAlpha, bocdPriorBeta}
	}
	for r := 0; r < n; r++ {
		row := flat[(r+1)*nf : (r+2)*nf : (r+2)*nf]
		src := b.stats[r]
		for i := range row {
			row[i] = ngUpdate(src[i], x[i])
		}
		rows[r+1] = row
	}
	b.post, b.stats, b.bufIdx = grown, rows, idx

	// 5. prune — this is what makes the update amortized O(1) rather than O(t)
	b.prune()

	// 6. declare. A boundary needs a segment on each side: the first observation after Reset
	//    starts the first run and is never a changepoint, however certain the posterior is that it
	//    started one.
	runLength := argmaxIdx(b.post)
	prob := changepointProb(b.post)
	declared := n > 1 && prob > bocdChangepointThreshold && b.since >= bocdMinTurnsBetweenDecls
	if declared {
		b.since = 0
	} else {
		b.since++
	}
	post := make([]float64, len(b.post))
	copy(post, b.post)
	b.last = ChangepointState{
		RunLength:       runLength,
		ProbChangepoint: prob,
		AtChangepoint:   declared,
		Posterior:       post,
	}
	return b.last
}

// changepointProb reads P(the newest observation started a new run) off a normalized posterior.
// Under the Adams–MacKay messages in observeVector, post[0] is the EMPTY-run hypothesis and equals
// the hazard identically — Σ_r post[r]·pred[r]·H over the same sum is H — because the newest
// observation carries no information about a boundary AFTER it. The informative entry is post[1],
// the run consisting of exactly the newest observation. A length-1 posterior is the Reset state:
// nothing has been observed, so the next observation starts a run with certainty.
func changepointProb(post []float64) float64 {
	if len(post) > 1 {
		return post[1]
	}
	return post[0]
}

// prune bounds the posterior. The hard cap folds the mass of any row past bocdMaxRunLength into
// the new top row rather than dropping it: on a stationary stretch longer than the cap that row
// holds nearly all the mass, and dropping it would collapse the posterior onto the empty run and
// re-declare a changepoint every bocdMaxRunLength turns. The top row's statistics then cover the
// bocdMaxRunLength−1 most recent observations — a bounded-memory window — and its run length
// saturates there. The epsilon tail is trimmed next, never below bocdMinPosteriorLen, and the
// survivors are renormalized.
func (b *bocd) prune() {
	if len(b.post) > bocdMaxRunLength {
		for _, v := range b.post[bocdMaxRunLength:] {
			b.post[bocdMaxRunLength-1] += v
		}
		b.post, b.stats = b.post[:bocdMaxRunLength], b.stats[:bocdMaxRunLength]
	}
	end := len(b.post)
	for end > bocdMinPosteriorLen && b.post[end-1] < bocdPruneEpsilon {
		end--
	}
	b.post, b.stats = b.post[:end], b.stats[:end]
	s := 0.0
	for _, v := range b.post {
		s += v
	}
	if s > 0 {
		for i := range b.post {
			b.post[i] /= s
		}
	}
}

// argmaxIdx returns the first index holding the maximum — deterministic on ties.
func argmaxIdx(xs []float64) int {
	best, bi := math.Inf(-1), 0
	for i, v := range xs {
		if v > best {
			best, bi = v, i
		}
	}
	return bi
}

// State returns the most recently computed ChangepointState without observing anything new.
func (b *bocd) State() ChangepointState { return b.last }

// MarshalBinary serializes the detector in the versioned, CRC32C-checked layout documented above.
// A detector built by NewBOCD always satisfies the layout's bounds; the guard exists so the format
// can never be written out of its own invariants.
func (b *bocd) MarshalBinary() ([]byte, error) {
	nf, nr := len(b.features), len(b.post)
	if nf < 1 || nf > bocdMaxFeatures || nr < 1 || nr > bocdMaxRunLength {
		return nil, fmt.Errorf("scheduler: BOCD state outside the format's bounds (F=%d, R=%d)", nf, nr)
	}
	size := bocdHeaderLen + nr*bocdFloatLen + nr*nf*ngRowLen + bocdCRCLen
	for _, name := range b.features {
		if len(name) > math.MaxUint8 {
			return nil, fmt.Errorf("scheduler: BOCD feature name %q too long for the format", name)
		}
		size += 1 + len(name)
	}
	buf := make([]byte, size)
	copy(buf, bocdMagic)
	binary.LittleEndian.PutUint16(buf[bocdOffVersion:], bocdFormatVersion)
	binary.LittleEndian.PutUint16(buf[bocdOffFeatures:], uint16(nf))
	binary.LittleEndian.PutUint64(buf[bocdOffHazard:], math.Float64bits(b.hazard))
	binary.LittleEndian.PutUint32(buf[bocdOffRunCount:], uint32(nr))
	binary.LittleEndian.PutUint32(buf[bocdOffSince:], saturateUint32(b.since))
	if b.last.AtChangepoint {
		buf[bocdOffDeclared] = 1
	}
	// bytes bocdOffReserved..bocdHeaderLen are the zero-valued reserved field.

	off := bocdHeaderLen
	for _, name := range b.features {
		buf[off] = uint8(len(name))
		off++
		off += copy(buf[off:], name)
	}
	for _, v := range b.post {
		binary.LittleEndian.PutUint64(buf[off:], math.Float64bits(v))
		off += bocdFloatLen
	}
	for _, row := range b.stats {
		for _, p := range row {
			binary.LittleEndian.PutUint64(buf[off:], math.Float64bits(p.Mu))
			binary.LittleEndian.PutUint64(buf[off+bocdFloatLen:], math.Float64bits(p.Kappa))
			binary.LittleEndian.PutUint64(buf[off+2*bocdFloatLen:], math.Float64bits(p.Alpha))
			binary.LittleEndian.PutUint64(buf[off+3*bocdFloatLen:], math.Float64bits(p.Beta))
			off += ngRowLen
		}
	}
	binary.LittleEndian.PutUint32(buf[off:], crc32.Checksum(buf[:off], crc32.MakeTable(crc32.Castagnoli)))
	return buf, nil
}

// saturateUint32 clamps a turn counter into the format's uint32 field.
func saturateUint32(v int) uint32 {
	if v < 0 {
		return 0
	}
	if uint64(v) >= math.MaxUint32 {
		return math.MaxUint32
	}
	return uint32(v)
}

// corruptState is the error every UnmarshalBinary rejection wraps: the daemon matches
// core.ErrNotFound, logs Loud, and calls Reset, exactly as sketch.Load does for a corrupt sketch.
func corruptState(detail string) error {
	return fmt.Errorf("scheduler: corrupt BOCD state: %s: %w", detail, core.ErrNotFound)
}

// f64At reads the little-endian float64 at data[off:off+8].
func f64At(data []byte, off int) float64 {
	return math.Float64frombits(binary.LittleEndian.Uint64(data[off:]))
}

// UnmarshalBinary restores a MarshalBinary buffer. It validates, in order: length ≥ 32; magic;
// version; 1 ≤ F ≤ 8; 1 ≤ R ≤ 512; every feature name known and none repeated (the constructor
// collapses a duplicate; a payload carrying one would run that stream's likelihood twice); the
// declared total length equal to the buffer length exactly; the CRC32C — and then, still in
// place, the header's fixed fields and the finiteness, normalisation and row invariants of the
// payload. Nothing is allocated until every check has passed, and the receiver is untouched on
// any failure.
func (b *bocd) UnmarshalBinary(data []byte) error {
	n := len(data)
	if n < bocdMinLen {
		return corruptState(fmt.Sprintf("buffer too short (%d bytes)", n))
	}
	if string(data[:len(bocdMagic)]) != bocdMagic {
		return corruptState("bad magic")
	}
	if v := binary.LittleEndian.Uint16(data[bocdOffVersion:]); v != bocdFormatVersion {
		return corruptState(fmt.Sprintf("unsupported version %d", v))
	}
	nf := int(binary.LittleEndian.Uint16(data[bocdOffFeatures:]))
	if nf < 1 || nf > bocdMaxFeatures {
		return corruptState(fmt.Sprintf("feature count %d out of range", nf))
	}
	runCount := binary.LittleEndian.Uint32(data[bocdOffRunCount:])
	if runCount < 1 || runCount > bocdMaxRunLength {
		return corruptState(fmt.Sprintf("run count %d out of range", runCount))
	}
	nr := int(runCount)

	var nameAt [bocdMaxFeatures][2]int
	off := bocdHeaderLen
	for i := 0; i < nf; i++ {
		if off >= n {
			return corruptState("truncated feature table")
		}
		l := int(data[off])
		off++
		if off+l > n {
			return corruptState("truncated feature table")
		}
		if _, ok := knownFeatures[string(data[off:off+l])]; !ok {
			return corruptState(fmt.Sprintf("unknown feature %q", data[off:off+l]))
		}
		for j := 0; j < i; j++ {
			if slices.Equal(data[nameAt[j][0]:nameAt[j][1]], data[off:off+l]) {
				return corruptState(fmt.Sprintf("duplicate feature %q", data[off:off+l]))
			}
		}
		nameAt[i] = [2]int{off, off + l}
		off += l
	}
	postOff := off
	statsOff := postOff + nr*bocdFloatLen
	crcOff := statsOff + nr*nf*ngRowLen
	if total := crcOff + bocdCRCLen; total != n {
		return corruptState(fmt.Sprintf("length %d does not match declared %d", n, total))
	}
	if got, want := crc32.Checksum(data[:crcOff], crc32.MakeTable(crc32.Castagnoli)), binary.LittleEndian.Uint32(data[crcOff:]); got != want {
		return corruptState("checksum mismatch")
	}

	// Structure verified; now the values, still without allocating.
	hazard := f64At(data, bocdOffHazard)
	if !(hazard > 0) || hazard >= 1 || math.IsNaN(hazard) {
		return corruptState("hazard out of range")
	}
	declared := data[bocdOffDeclared]
	if declared > 1 {
		return corruptState(fmt.Sprintf("declared byte %d is not 0|1", declared))
	}
	for _, r := range data[bocdOffReserved:bocdHeaderLen] {
		if r != 0 {
			return corruptState("reserved bytes not zero")
		}
	}
	mass := 0.0
	for r := 0; r < nr; r++ {
		v := f64At(data, postOff+r*bocdFloatLen)
		if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
			return corruptState(fmt.Sprintf("posterior[%d] is not a probability", r))
		}
		mass += v
	}
	if !(mass > 0) || math.IsInf(mass, 0) {
		return corruptState("posterior has no mass")
	}
	if math.Abs(mass-1) > bocdMassTolerance {
		return corruptState(fmt.Sprintf("posterior mass %g is not normalised", mass))
	}
	for r := 0; r < nr; r++ {
		// Row r has absorbed exactly r observations, so its Kappa and Alpha are fixed by r;
		// the lgamma tables rely on it.
		wantKappa := bocdPriorKappa + float64(r)
		wantAlpha := bocdPriorAlpha + float64(r)/2
		for f := 0; f < nf; f++ {
			o := statsOff + (r*nf+f)*ngRowLen
			mu, kappa, alpha, beta := f64At(data, o), f64At(data, o+bocdFloatLen),
				f64At(data, o+2*bocdFloatLen), f64At(data, o+3*bocdFloatLen)
			if math.IsNaN(mu) || math.IsInf(mu, 0) || kappa != wantKappa || alpha != wantAlpha ||
				!(beta > 0) || math.IsInf(beta, 0) {
				return corruptState(fmt.Sprintf("statistics[%d][%d] inconsistent", r, f))
			}
		}
	}

	// Everything checked: build the new state, then swap it in.
	features := make([]string, nf)
	for i := 0; i < nf; i++ {
		features[i] = string(data[nameAt[i][0]:nameAt[i][1]])
	}
	post := make([]float64, nr)
	for r := range post {
		post[r] = f64At(data, postOff+r*bocdFloatLen)
	}
	flat := make([]ngParams, nr*nf)
	stats := make([][]ngParams, nr)
	for r := range stats {
		row := flat[r*nf : (r+1)*nf : (r+1)*nf]
		for f := range row {
			o := statsOff + (r*nf+f)*ngRowLen
			row[f] = ngParams{
				Mu:    f64At(data, o),
				Kappa: f64At(data, o+bocdFloatLen),
				Alpha: f64At(data, o+2*bocdFloatLen),
				Beta:  f64At(data, o+3*bocdFloatLen),
			}
		}
		stats[r] = row
	}
	snapshot := make([]float64, nr)
	copy(snapshot, post)

	b.hazard = hazard
	b.features = features
	b.post = post
	b.stats = stats
	b.bufIdx = -1
	b.since = int(binary.LittleEndian.Uint32(data[bocdOffSince:]))
	b.buildLgammaTables()
	b.last = ChangepointState{
		RunLength:       argmaxIdx(post),
		ProbChangepoint: changepointProb(post),
		AtChangepoint:   declared == 1,
		Posterior:       snapshot,
	}
	return nil
}

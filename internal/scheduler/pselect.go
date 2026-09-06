package scheduler

import (
	"cmp"
	"slices"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
)

// maxScoredCandidates bounds the argmax sweep. Qompack.md §5.4: "Twenty candidates, not
// 167,000." The Runtime already caps its own assembly; this is defence in depth so a
// misbehaving caller cannot make Evaluate super-linear.
const maxScoredCandidates = 32

type scored struct {
	c          Candidate
	tail       float64 // max(0, n − Pos): the rewritten suffix length in tokens
	reclaim    float64
	rewrite    float64
	distortion float64
	score      float64
}

// eligible implements "candidates = changepoint boundaries ∩ API-round boundaries" (§8.4).
// Every Candidate the Runtime emits already sits on a changepoint boundary; RoundBoundary
// carries the second half of the intersection. If the intersection is empty we relax to the
// full set rather than refusing to cut — refusing would leave the session with no legal p
// exactly when the trigger says it must act — and record the relaxation in Breakdown.
//
// The filter runs IN PLACE on cands, which the caller must therefore own: Evaluate hands it
// the private copy prepareCandidates made, so the caller's Inputs are never touched and the
// sweep stays within its allocation budget. Order is preserved. When nothing is a round
// boundary no element has been written, and cands itself is returned unchanged.
func eligible(cands []Candidate) (out []Candidate, relaxed bool) {
	if len(cands) == 0 {
		return nil, false
	}
	out = cands[:0]
	for _, c := range cands {
		if c.RoundBoundary {
			out = append(out, c)
		}
	}
	if len(out) == 0 {
		return cands, true
	}
	return out, false
}

// scoreCandidates implements §8.4 verbatim:
//
//	reclaimable(p) = Σ tokens of droppable blocks after p   (supplied by the Runtime)
//	rewrite(p)     = w · (n − p)      × cacheFactor         ("0 if cache cold or expiring")
//	distortion(p)  = λ · segment_coupling(p)
//	score(p)       = reclaimable(p)·r − rewrite(p) − distortion(p)
//
// r and w come from the CacheRegime the session is actually billed at (ruling R4): the
// config keys are Appendix C's five-minute floor, and the regime is what the ladder in
// cacheregime.go resolved from them. They are never literals (00-ARCHITECTURE §11.6).
func scoreCandidates(cands []Candidate, n core.Tokens, cacheFactor, lambda float64,
	reg CacheRegime,
) []scored {
	return scoreCandidatesInto(make([]scored, 0, len(cands)), cands, n, cacheFactor, lambda, reg)
}

// scoreCandidatesInto is scoreCandidates appending onto dst, so Evaluate can hand it a
// stack buffer sized for maxScoredCandidates and keep the sweep off the heap.
func scoreCandidatesInto(dst []scored, cands []Candidate, n core.Tokens, cacheFactor, lambda float64,
	reg CacheRegime,
) []scored {
	r := reg.ReadMultiplier
	w := reg.WriteMultiplier
	out := dst
	for _, c := range cands {
		tail := float64(n) - float64(c.Pos)
		if tail < 0 {
			tail = 0
		}
		reclaim := float64(c.ReclaimableTokens) * r
		rewrite := w * tail * cacheFactor
		dist := 0.0
		if lambda > 0 {
			dist = lambda * float64(c.Coupling)
		}
		out = append(out, scored{
			c: c, tail: tail, reclaim: reclaim, rewrite: rewrite,
			distortion: dist, score: reclaim - rewrite - dist,
		})
	}
	return out
}

// chooseP is the argmax. Tie-breaking encodes §5.4's bimodality explicitly:
//   - cold cache AND cfg.Idle.DeepCutWhenCold → prefer the SMALLEST Pos (deep cut is free)
//   - otherwise                               → prefer the LARGEST Pos (edit as late as possible)
//
// Ties are compared with an exact float equality after both scores are finite; scores are
// produced by the same arithmetic on the same machine, so exact comparison is correct here.
func chooseP(ss []scored, ttl TTLState, cfg config.SchedulerCfg) (scored, bool) {
	if len(ss) == 0 {
		return scored{}, false
	}
	deep := ttl == TTLCold && cfg.Idle.DeepCutWhenCold
	best := ss[0]
	for _, s := range ss[1:] {
		switch {
		case s.score > best.score:
			best = s
		case s.score == best.score:
			if (deep && s.c.Pos < best.c.Pos) || (!deep && s.c.Pos > best.c.Pos) {
				best = s
			}
		}
	}
	return best, true
}

// prepareCandidates copies cands, sorts the copy ascending by Pos, and reports
// whether reclaimable(p) violated §5.4's
// monotonicity ("Reclaimable tokens are non-increasing in p"). A violation is a Runtime bug,
// not a reason to refuse to decide, so it is surfaced in Breakdown and the sweep continues.
// The copy is the only allocation; the stable sort works in place.
func prepareCandidates(cands []Candidate) (out []Candidate, nonMonotonic bool) {
	return prepareCandidatesInto(nil, cands)
}

// prepareCandidatesInto is prepareCandidates copying onto dst (which must be empty), so
// Evaluate can hand it a stack buffer and pay for a heap copy only when a caller supplies more
// candidates than the buffer holds. The caller's cands are never touched.
func prepareCandidatesInto(dst, cands []Candidate) (out []Candidate, nonMonotonic bool) {
	if len(cands) == 0 {
		return nil, false
	}
	out = dst
	out = append(out, cands...)
	slices.SortStableFunc(out, func(a, b Candidate) int { return cmp.Compare(a.Pos, b.Pos) })
	for i := 1; i < len(out); i++ {
		if out[i].ReclaimableTokens > out[i-1].ReclaimableTokens {
			nonMonotonic = true
			break
		}
	}
	return out, nonMonotonic
}

// capHighestPos keeps the highest-Pos maxScoredCandidates of an ascending-sorted set. It runs
// AFTER eligible (ruling R44): capping before the round-boundary intersection could discard
// every eligible candidate and then "relax" onto ineligible ones the cap had let through.
func capHighestPos(cands []Candidate) []Candidate {
	if len(cands) > maxScoredCandidates {
		return cands[len(cands)-maxScoredCandidates:]
	}
	return cands
}

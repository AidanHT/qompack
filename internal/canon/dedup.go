package canon

import (
	"math"

	"github.com/qompack/qompack/internal/sketch"
)

// Strategy is how SP-06's store should write a result that has a prior version on disk.
type Strategy uint8

const (
	// StrategyFull stores the canonical text outright.
	StrategyFull Strategy = iota
	// StrategyDelta stores only the difference against the prior version.
	StrategyDelta
)

// String renders a Strategy for logs and metrics.
func (s Strategy) String() string {
	if s == StrategyDelta {
		return "delta"
	}
	return "full"
}

// deltaFrameBytes is the fixed overhead a stored delta carries beyond the changed bytes
// themselves: the record header, the prior-root reference and the length framing. It is a
// deliberate over-estimate, because the failure mode it guards against — choosing a delta that
// turns out bigger than the full text — costs both space and a decode hop on every read, while
// over-estimating merely stores a full copy that chunk-level dedup will collapse anyway.
const deltaFrameBytes = 64

// DedupDecision is what Decide concludes about storing a canonical payload that has a
// near-duplicate prior version. SP-06 consumes it to populate store.NearDupInfo.
type DedupDecision struct {
	// NearDup reports whether the two payloads passed the Jaccard threshold.
	NearDup bool
	// Jaccard is the estimated Jaccard similarity the decision was made from.
	Jaccard float64
	// Strategy is how to store the payload.
	Strategy Strategy
	// EstDeltaBytes is the estimated size of the delta record, including framing.
	EstDeltaBytes int
}

// NearDup reports whether a and b are near-duplicates at threshold, and the estimated Jaccard
// similarity behind that answer.
//
// It returns both because the boolean alone is not enough for SP-06: store.NearDupInfo records the
// score so a later dedup-ratio investigation can tell "just under the threshold" from "nothing
// like it", and Decide needs the score anyway to size the delta.
func NearDup(a, b sketch.Signature, threshold float64) (bool, float64) {
	return a.IsNearDup(b, threshold), a.Jaccard(b)
}

// Decide chooses between storing a canonical payload in full and storing it as a delta against a
// near-duplicate prior version. It is the pure decision function behind Qompack.md §8.1's
// "same test suite, one new failure" case, and SP-06 calls it with the threshold from
// store.canonicalize.minhash.nearDupThreshold.
//
// The size model: a Jaccard similarity of j over two documents means roughly a (1-j) fraction of
// the longer one differs, so the delta costs about (1-j)*max(canonLen, priorLen) bytes plus fixed
// framing. This is an estimate from a sketch, not a diff — computing the real delta to decide
// whether to compute the real delta would defeat the purpose on the hot path.
//
// The rule is EstDeltaBytes*2 < canonLen, not EstDeltaBytes < canonLen. A delta that saves only a
// little is a bad trade: it is worth storing only when it is under half the size of the canonical
// text, because below that margin the chunk-level dedup FastCDC already performs is both cheaper
// and simpler than a second, chained representation with its own decode hop and its own
// dependency on a prior object staying alive through garbage collection.
//
// threshold is never written as a literal in this package: Appendix C's default (0.9) is one of
// 00-ARCHITECTURE.md §11.6's forbidden literals precisely so it can only arrive from config.
func Decide(jaccard float64, canonLen, priorLen int, threshold float64) DedupDecision {
	d := DedupDecision{Jaccard: jaccard, Strategy: StrategyFull}
	if canonLen <= 0 || priorLen <= 0 {
		return d
	}
	d.NearDup = jaccard >= threshold

	longer := canonLen
	if priorLen > longer {
		longer = priorLen
	}
	d.EstDeltaBytes = int(math.Round((1-jaccard)*float64(longer))) + deltaFrameBytes

	if d.NearDup && d.EstDeltaBytes*2 < canonLen {
		d.Strategy = StrategyDelta
	}
	return d
}

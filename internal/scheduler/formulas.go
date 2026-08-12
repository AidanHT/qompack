package scheduler

import "math"

// YoungDaly returns the Young-Daly optimal checkpoint interval I* = sqrt(2*delta*M) (Qompack.md
// §6.7, Appendix A; 00-ARCHITECTURE.md §5.13), where deltaSeconds is the measured cost of taking
// a checkpoint and mtbfSeconds is the expected time to a forced compaction at the current burn
// rate. It is a fully specified pure function, so SP-01 implements it for real rather than
// stubbing it (00-ARCHITECTURE.md §14.1): SP-12 and the eval harness both need a working formula
// before SP-12's own package lands.
//
// A non-positive input makes the formula meaningless (there is nothing to amortize against), so
// YoungDaly reports 0 rather than NaN or a negative interval.
func YoungDaly(deltaSeconds, mtbfSeconds float64) float64 {
	if deltaSeconds <= 0 || mtbfSeconds <= 0 {
		return 0
	}
	return math.Sqrt(2 * deltaSeconds * mtbfSeconds)
}

// SkiRentalShouldWrite applies the ski-rental cache-write rule (Qompack.md §5.6,
// 00-ARCHITECTURE.md §5.13): write the cache when the expected number of remaining reads exceeds
// w/r, the ratio of the prompt cache's write cost to its read cost. w/r is computed here, never
// hardcoded — §5.1's own worked example works out to roughly twelve and a half remaining reads,
// but r and w are config keys that may change (00-ARCHITECTURE.md D11, §11.6), so neither that
// ratio nor its operands may ever appear as a literal in this package. §12 lists "cache
// multipliers change" as a live risk; computing the threshold is what makes that a config edit
// rather than a code change.
//
// A non-positive r makes the ratio undefined, so SkiRentalShouldWrite conservatively reports
// false rather than dividing by zero or by a negative number.
func SkiRentalShouldWrite(expectedReads, r, w float64) bool {
	if r <= 0 {
		return false
	}
	return expectedReads > w/r
}

// pSelectionAvailable is the ship-order guard behind the closing note's priority 3 ("do not ship
// slicing or submodular selection before p-selection"): it stays false until SP-12 flips it,
// which is what makes analyzer.NewSelector refuse to construct a Selector in every build that
// predates a real scheduler.
var pSelectionAvailable = false

// PSelectionAvailable reports whether this build has a real p-selection implementation
// (00-ARCHITECTURE.md §5.13, closing note priority 3). analyzer.NewSelector's constructor
// validation calls this directly; SP-12 is the only subplan permitted to flip
// pSelectionAvailable to true.
func PSelectionAvailable() bool { return pSelectionAvailable }

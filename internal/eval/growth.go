package eval

import (
	"fmt"
	"math"
	"sort"
)

// Growth-guardrail thresholds. minGrowthSamples and minGrowthSpan are what stop the fit from
// pretending to a precision it does not have: an ordinary least squares line through four points
// spanning 2x cannot tell an exponent of 0.9 from one of 1.0.
const (
	minGrowthSamples = 6
	minGrowthSpan    = 8.0
	sublinearMax     = 0.95
)

// CheckSublinearGrowth is §11.3's "store growth sublinear in session length after dedup".
//
// The regressor is RawBytes, with Turn as a validity guard, and the substitution is deliberate.
// Turn count alone is a bad x-axis — one 40 MB test run and one 200-byte Grep are both "one turn"
// — while raw bytes is the quantity dedup is actually asked to beat. That substitution is only
// honest while raw bytes grows with the session, so a series where it does not is rejected as
// inconclusive rather than fitted anyway.
//
// An inconclusive verdict is NOT a pass: the gate fails on it, because an unmeasurable guardrail
// is not a passing guardrail.
func CheckSublinearGrowth(samples []StatsSample) GrowthResult {
	usable := make([]StatsSample, 0, len(samples))
	for _, s := range samples {
		if s.RawBytes > 0 && s.Bytes > 0 {
			usable = append(usable, s)
		}
	}
	sort.SliceStable(usable, func(i, j int) bool { return usable[i].Turn < usable[j].Turn })

	res := GrowthResult{Samples: len(usable)}
	if len(usable) < minGrowthSamples {
		res.Reason = fmt.Sprintf(
			"inconclusive: %d usable samples, at least %d are needed to fit an exponent",
			len(usable), minGrowthSamples)
		return res
	}

	for i := 1; i < len(usable); i++ {
		if usable[i].RawBytes < usable[i-1].RawBytes {
			res.Reason = fmt.Sprintf(
				"inconclusive: rawBytes not monotone in turn (turn %d holds %d bytes, "+
					"turn %d only %d); the proxy for session length is invalid",
				usable[i-1].Turn, usable[i-1].RawBytes, usable[i].Turn, usable[i].RawBytes)
			return res
		}
	}

	lo, hi := usable[0].RawBytes, usable[len(usable)-1].RawBytes
	res.RawSpan = float64(hi) / float64(lo)
	if res.RawSpan < minGrowthSpan {
		res.Reason = fmt.Sprintf(
			"inconclusive: rawBytes spans only %.2fx, and a fit needs at least 8x to separate a "+
				"sublinear exponent from a linear one", res.RawSpan)
		return res
	}

	// Ordinary least squares on ln(Bytes) = α·ln(RawBytes) + c. α is the growth exponent: 1 means
	// dedup achieved nothing, and anything at or under 0.95 is the sublinear the guardrail wants.
	var sumX, sumY float64
	for _, s := range usable {
		sumX += math.Log(float64(s.RawBytes))
		sumY += math.Log(float64(s.Bytes))
	}
	n := float64(len(usable))
	meanX, meanY := sumX/n, sumY/n

	var num, den float64
	for _, s := range usable {
		dx := math.Log(float64(s.RawBytes)) - meanX
		num += dx * (math.Log(float64(s.Bytes)) - meanY)
		den += dx * dx
	}
	if den == 0 {
		res.Reason = "inconclusive: every sample reports the same rawBytes, so the fit has no slope"
		return res
	}

	res.Exponent = num / den
	res.Sublinear = res.Exponent <= sublinearMax
	return res
}

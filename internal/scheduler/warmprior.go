package scheduler

import (
	"fmt"
	"math"
)

// This file is SP-16 §1's warm-start (O4) policy: given history from earlier sessions of this
// project, how much weight — if any — may it carry into the current one?
//
// The MECHANISM already exists and is not this file's business. sketch.CMS.MergeFrom merges two
// tables and sketch.CMS.Scale decays one, and 00-ARCHITECTURE.md §2.5 lists MergeFrom as the
// reason the Count-Min sketch is implemented in-house at all. What has never existed is the
// decision in front of them, and shipping the mechanism without it is how a project's historical
// hot files come to outvote the file the agent is editing right now.
//
// Three rules shape everything below, and all three come from SP-16 §1's "an optional warm prior
// is a labeled statistical candidate, not inherited truth".
//
// A prior is never AUTHORITY. WarmStart cannot authorize itself: PriorEvidence.Authorized is
// supplied by the caller, which is the only party that can see the scope question, and a prior
// that arrives unauthorized is refused outright rather than discounted. This package is
// foundation-only (see doc.go's purity contract) and may not import negknow, so it takes the
// scope decision as a value the way Evaluate takes Inputs.
//
// A prior is never CERTAINTY. Weight is capped at half of one observation of the present, so the
// first thing a session actually measures outweighs the whole of its history more than two to
// one, and the cap decays further with the age and thinness of the evidence behind it.
// Blend then hands the observed value back unchanged once enough of it exists, so a warm start
// influences only the part of a session that has nothing better to go on.
//
// A prior is never SILENT. Every decision carries a Label naming what it did and what it was
// based on, so a warm-started number can be told apart from a measured one in /qompack:status and
// in an ablation report. A prior that cannot be labeled is not applied.

// WarmStartPolicy bounds how much a warm prior may ever be worth.
//
// The numbers are deliberately NOT config keys yet. runtime.phase7.reuse.warmPrior is gated off
// and its M6-G16-D gate is precisely "compare this against a simple baseline on held-out tasks";
// promoting an unevaluated policy constant to a user-facing key would publish a knob before
// anyone knows whether the mechanism it tunes is worth having. DefaultWarmStartPolicy is the
// baseline the ablation measures against, and the commit that passes the gate is the one that
// decides which of these — if any — becomes a key.
type WarmStartPolicy struct {
	// MinSessions is how many earlier sessions must have contributed before history counts as a
	// prior at all. One session's data is that session's data, not a prior over the project.
	MinSessions int
	// MinObservations is the same floor on total observations, so a handful of turns spread over
	// many sessions is refused too.
	MinObservations int
	// HalfLifeSeconds is how quickly weight decays with the age of the newest contributing
	// observation. Zero or negative disables ageing, which is what a caller replaying a fixed
	// corpus wants and what a live session must never set.
	HalfLifeSeconds float64
	// MaxWeight caps the weight any prior may carry, and Clamp holds it at or below
	// maxWeightCeiling. Blend measures weight in units of ONE observation of the present, so the
	// ceiling is what stops "history outvotes what the agent is touching right now" — the exact
	// failure O4 has to avoid.
	MaxWeight float64
	// MaxAgeSeconds is the horizon past which a prior is refused rather than merely discounted. A
	// project whose last session was months ago is not weakly informative about this one; it is
	// silent about it.
	MaxAgeSeconds float64
}

// maxWeightCeiling is the largest weight WarmStartPolicy.MaxWeight may take, inclusive.
//
// Blend weighs a prior against observations of the present in units of one observation, so this
// says the whole of a project's history is worth at most HALF of one thing the current session
// measured. That is the bound that makes a warm start a nudge: the first real observation
// outweighs every prior more than two to one, and there is no policy — however mis-set — that
// lets history reach parity with a single present-tense fact.
const maxWeightCeiling = 0.5

// DefaultWarmStartPolicy is the baseline M6-G16-D measures against.
//
// Every number is a starting point chosen to be conservative rather than a tuned value, and the
// ablation may well conclude that the honest answer is to leave warmPrior off. Three sessions and
// thirty observations are the smallest counts at which "the project tends to" means anything at
// all; a one-day half-life and a two-week horizon match how quickly a codebase's hot set moves;
// and a quarter is a deliberately small maximum weight, half of what the ceiling would even
// allow.
func DefaultWarmStartPolicy() WarmStartPolicy {
	return WarmStartPolicy{
		MinSessions:     3,
		MinObservations: 30,
		HalfLifeSeconds: 24 * 60 * 60,
		MaxWeight:       0.25,
		MaxAgeSeconds:   14 * 24 * 60 * 60,
	}
}

// Clamp returns p with every field forced into a range that cannot produce an unsafe decision: no
// negative floors, no weight above maxWeightCeiling, and no negative horizons.
//
// It is applied by WarmStart rather than trusted from the caller, because the failure mode of a
// mis-set policy is silent — a MaxWeight of 2 does not error, it just lets history win — and a
// policy assembled from configuration or from a test fixture must not be able to express that.
func (p WarmStartPolicy) Clamp() WarmStartPolicy {
	p.MinSessions = max(p.MinSessions, 1)
	p.MinObservations = max(p.MinObservations, 1)
	if p.HalfLifeSeconds < 0 || math.IsNaN(p.HalfLifeSeconds) {
		p.HalfLifeSeconds = 0
	}
	if math.IsNaN(p.MaxWeight) || p.MaxWeight <= 0 {
		p.MaxWeight = 0
	}
	p.MaxWeight = math.Min(p.MaxWeight, maxWeightCeiling)
	if p.MaxAgeSeconds < 0 || math.IsNaN(p.MaxAgeSeconds) {
		p.MaxAgeSeconds = 0
	}
	return p
}

// PriorEvidence is what is actually known about the history behind a candidate warm prior.
//
// Authorized is a value rather than something this package computes, and that is the purity
// contract doing its job: whether history from another session may be read here is a scope and
// consent question (SP-16 §1), the types that answer it live in internal/negknow, and doc.go
// forbids this package from importing it. The composition root that assembles Inputs is the same
// place that can call negknow.Applies, so it passes the answer in.
type PriorEvidence struct {
	// Sessions is how many distinct earlier sessions contributed.
	Sessions int
	// Observations is the total number of observations behind the prior.
	Observations int
	// AgeSeconds is how old the NEWEST contributing observation is. Using the newest rather than
	// the mean is deliberate: a prior is refused for having gone quiet, and a long tail of old
	// data should not make recent data look stale.
	AgeSeconds float64
	// Authorized reports that the caller established, through the scope and authorization gate,
	// that this history may be read in the current scope. False is a refusal, not a discount.
	Authorized bool
}

// WarmStartDecision is WarmStart's answer: whether to apply a prior, how heavily, and why.
//
// Weight is the number a caller hands sketch.CMS.Scale before merging, and it is 0 whenever Apply
// is false, so a caller that ignores Apply and multiplies by Weight still gets the right
// behaviour. Label is never empty.
type WarmStartDecision struct {
	// Apply reports whether any history may be carried in.
	Apply bool
	// Weight is the factor history is scaled by before it is merged, in [0, maxWeightCeiling].
	Weight float64
	// Label names what was decided and what it rests on. It is what /qompack:status prints beside
	// a warm-started number so it can be told apart from a measured one.
	Label string
}

// WarmStart decides how much weight the history in e may carry under p.
//
// The order is a refusal ladder, and every rung returns rather than discounting, because each one
// describes history that is not weakly informative but silent:
//
//  1. UNAUTHORIZED. The caller did not establish that this history may be read here. Nothing
//     below applies; there is no discount rate for missing consent.
//  2. TOO THIN. Fewer than MinSessions sessions or MinObservations observations. A prior is a
//     statement about a project's tendencies, and neither one session nor a handful of turns is
//     one.
//  3. TOO OLD. Older than MaxAgeSeconds.
//  4. Otherwise the weight is MaxWeight halved once per HalfLifeSeconds of age, which is a
//     nudge that fades rather than a vote that persists.
//
// A NaN or negative age is treated as unknown and refused at step 3: an age nobody measured is
// not a fresh one.
func WarmStart(e PriorEvidence, p WarmStartPolicy) WarmStartDecision {
	p = p.Clamp()

	if !e.Authorized {
		return WarmStartDecision{Label: "no warm start: history is not authorized in this scope"}
	}
	if e.Sessions < p.MinSessions || e.Observations < p.MinObservations {
		return WarmStartDecision{Label: fmt.Sprintf(
			"no warm start: %d session(s) and %d observation(s) are below the %d/%d floor",
			e.Sessions, e.Observations, p.MinSessions, p.MinObservations)}
	}
	if math.IsNaN(e.AgeSeconds) || e.AgeSeconds < 0 {
		return WarmStartDecision{Label: "no warm start: the age of the newest observation is unknown"}
	}
	if p.MaxAgeSeconds > 0 && e.AgeSeconds > p.MaxAgeSeconds {
		return WarmStartDecision{Label: fmt.Sprintf(
			"no warm start: newest observation is %.0fs old, past the %.0fs horizon",
			e.AgeSeconds, p.MaxAgeSeconds)}
	}

	w := p.MaxWeight
	if p.HalfLifeSeconds > 0 {
		w *= math.Exp2(-e.AgeSeconds / p.HalfLifeSeconds)
	}
	if w <= 0 || math.IsNaN(w) {
		return WarmStartDecision{Label: "no warm start: decayed weight is not positive"}
	}
	return WarmStartDecision{
		Apply:  true,
		Weight: w,
		Label: fmt.Sprintf("warm prior at weight %.4f from %d session(s), %d observation(s), newest %.0fs old",
			w, e.Sessions, e.Observations, e.AgeSeconds),
	}
}

// Blend combines a warm prior with what the current session has actually observed, weighting the
// prior by d.Weight against observedCount observations of its own.
//
// The formula is a count-weighted mean in which the prior is worth d.Weight of ONE observation:
//
//	(prior·Weight + observed·observedCount) / (Weight + observedCount)
//
// so the prior is at its most influential when observedCount is 0 and is swamped as soon as the
// session has anything of its own. That is the "not inherited truth" property expressed
// arithmetically: at observedCount 0 the answer is the prior, at observedCount 1 the prior is
// already worth at most a quarter of what this session just measured, and by ten observations it
// has moved the answer by under three percent of the gap.
//
// Every degenerate case returns observed UNCHANGED — a decision that did not apply, a
// non-positive or NaN weight, a negative observedCount, a NaN prior. That single fallback is the
// point: the answer a caller gets when the prior cannot be used is the one it would have had
// without this file at all, never a substituted default. A NaN observed therefore propagates out
// as NaN rather than becoming a fabricated zero.
func Blend(prior, observed float64, observedCount int, d WarmStartDecision) float64 {
	if !d.Apply || d.Weight <= 0 || observedCount < 0 ||
		math.IsNaN(prior) || math.IsNaN(d.Weight) {
		return observed
	}
	den := d.Weight + float64(observedCount)
	if den <= 0 {
		return observed
	}
	return (prior*d.Weight + observed*float64(observedCount)) / den
}

package daemon

import (
	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/negknow"
)

// This file applies SP-16 §1's gate: given eliminations recorded somewhere else, which of them may
// be offered here, and what does the transcript say about the rest?
//
// The decision is negknow.Applies and none of it is re-implemented. What this file adds is the
// three things a composition root owns — the switch, the report, and the refusal to let a
// disabled feature look like an empty one.
//
// REPORT-ONLY IS THE FIRST ROLLOUT STAGE, and it is a state this type can actually be in. The
// subplan's rollout says "start report-only scoped candidates, then opt-in bounded
// retrieval/promotion", so Consider ALWAYS evaluates every candidate and always fills in the
// report; runtime.phase7.reuse.scopedCandidates decides only whether the allowed ones are handed
// back. That is what makes the report worth reading before the switch is flipped: an operator can
// see what reuse would have offered without any of it reaching a session.
//
// A DISABLED FEATURE AND AN EMPTY RESULT ARE DIFFERENT. ReuseReport.Enabled says which one
// happened, because "scoped reuse offered nothing" and "scoped reuse is off" call for opposite
// responses, and a caller that could not tell them apart would read a gated-off build as evidence
// that there is nothing to reuse.
//
// THE GRANT IS SUPPLIED, NEVER SYNTHESIZED. There is no code path here that constructs a
// negknow.Grant out of what it observed. A grant is a user decision, and a daemon that could mint
// one from its own observations would be the content promoting its own authority that
// core.Authority exists to forbid.

// ReuseCandidate is one elimination from somewhere else, together with everything the gate needs
// to decide about it.
//
// Origin, Life and Authority all travel WITH the record rather than being looked up from it,
// because Record's json tags are frozen (internal/negknow/record.go) and none of the three has a
// field there. Whoever loads a foreign record is the party that knows where it came from.
type ReuseCandidate struct {
	// Record is the elimination itself.
	Record negknow.Record
	// Origin is the scope it was observed in.
	Origin negknow.ReuseScope
	// Life is what has happened to it since.
	Life negknow.Lifecycle
	// Authority is the record's own standing. An unset value is treated as unsettled, which is
	// what negknow.Applies does with it.
	Authority core.Authority
	// Access is the outcome of reaching the record, in SP-20's vocabulary. The zero value means no
	// access attempt is being reported.
	Access core.EvidenceOutcome
}

// ReusableCandidate is one candidate the gate allowed, with the decision that qualified it.
//
// Applicability is carried rather than discarded because a consumer that renders a reused
// elimination has to render its origin and retained authority with it — that is the mechanism
// behind "cross-session summaries remain attributed derived claims", and dropping it is how
// another session's conclusion becomes this one's intent.
type ReusableCandidate struct {
	Record        negknow.Record
	Applicability negknow.Applicability
}

// ReuseReport is the applicability transcript M6-G16-A asks for: every candidate considered, what
// was decided about it, and why.
type ReuseReport struct {
	// Enabled reports whether runtime.phase7.reuse.scopedCandidates was on. When it is false the
	// report is complete but Consider returned no candidates: this is the report-only stage, not
	// an empty result.
	Enabled bool
	// Considered is how many candidates were evaluated.
	Considered int
	// Allowed is how many the gate permitted — whether or not the switch let them through.
	Allowed int
	// ByRelation counts candidates by the relation observation established, so a transcript can
	// show that (say) everything came from a sibling worktree.
	ByRelation map[negknow.Relation]int
	// Withheld and Denied count the two refusals separately, because one can be resolved by
	// observing more and the other cannot.
	Withheld, Denied int
	// Omissions lists every distinct reason a candidate was refused, with the count of candidates
	// it applied to. Distinct reasons rather than one entry per candidate: a hundred records from
	// one unauthorized worktree is one problem, and a transcript that repeated it a hundred times
	// would bury the other three.
	Omissions map[string]int
}

// ScopedReuse applies the gate for one project, in one place.
//
// It holds no lock and no mutable state beyond its configuration: Consider is a pure function of
// its argument and the fields set at construction, so two sessions may share one value and a
// transcript is reproducible from its inputs.
type ScopedReuse struct {
	enabled bool
	here    negknow.ReuseScope
	grant   negknow.Grant
	depCov  core.Omission
}

// NewScopedReuse returns the gate for a project.
//
// here is the scope ObserveScope produced for the current session. grant is the authorization a
// user supplied, or the zero Grant when none was; the zero Grant authorizes nothing, which is why
// forgetting to pass one is safe. depCoverage is the elimination ledger's current dependency
// watermark (negknow.Health.DependencyCoverage), so that a ledger which could not verify freshness
// withholds cross-scope reuse rather than affirming it.
func NewScopedReuse(cfg config.Phase7ReuseCfg, here negknow.ReuseScope, grant negknow.Grant,
	depCoverage core.Omission,
) *ScopedReuse {
	return &ScopedReuse{
		enabled: cfg.ScopedCandidates,
		here:    here,
		grant:   grant,
		depCov:  depCoverage,
	}
}

// Consider evaluates every candidate and returns the ones that may be offered, plus the transcript.
//
// now is the caller's clock reading; negknow.Applies has no clock of its own, which is what makes
// the transcript reproducible.
//
// The returned slice is empty — and the report still complete — whenever the switch is off. A
// caller must therefore branch on ReuseReport.Enabled, not on the length of the slice, to tell a
// disabled feature from one that found nothing.
func (s *ScopedReuse) Consider(now core.UnixMilli, candidates []ReuseCandidate) ([]ReusableCandidate, ReuseReport) {
	rep := ReuseReport{
		Enabled:    s.enabled,
		Considered: len(candidates),
		ByRelation: map[negknow.Relation]int{},
		Omissions:  map[string]int{},
	}

	var out []ReusableCandidate
	for _, c := range candidates {
		d := negknow.Applies(negknow.ReuseRequest{
			From:               c.Origin,
			To:                 s.here,
			Life:               c.Life,
			EvidenceAuthority:  c.Authority,
			Grant:              s.grant,
			Access:             c.Access,
			DependencyCoverage: s.depCov,
			Now:                now,
		})
		rep.ByRelation[d.Relation]++
		for _, o := range d.Omissions {
			rep.Omissions[o.Reason]++
		}

		switch d.Reuse {
		case negknow.ReuseAllowed:
			rep.Allowed++
			if s.enabled {
				out = append(out, ReusableCandidate{Record: c.Record, Applicability: d})
			}
		case negknow.ReuseDenied:
			rep.Denied++
		default:
			rep.Withheld++
		}
	}
	return out, rep
}

// Enabled reports whether the scoped-candidate switch is on. It exists so a caller can decide
// whether to do the work of loading foreign records at all — the report-only stage is worth
// paying for deliberately, not by accident on every rehydration.
func (s *ScopedReuse) Enabled() bool { return s.enabled }

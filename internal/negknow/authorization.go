package negknow

import (
	"fmt"

	"github.com/qompack/qompack/internal/core"
)

// This file is SP-16 §1's decision: given where evidence came from (scope.go), what has happened
// to it since (expiry.go), and what someone actually authorized, may it be reused HERE?
//
// Applies is the whole gate, and it is a pure function so that an applicability transcript is
// reproducible from its inputs alone — which is what M6-G16-A asks for. It does no I/O, consults
// no clock of its own and holds no state; a caller supplies now.
//
// Three properties are load-bearing.
//
// Permission is never the zero value. Reusability's zero is ReuseWithheld, Relation's is
// RelationUnknown, Grant's Authority zero is invalid, and Disposition's is DispositionUncertain.
// A caller that forgets to fill something in gets a refusal, never a licence. This is the single
// most important thing in the file: every other rule is enforcement, but this is what makes
// forgetting safe.
//
// Reuse never promotes authority. A conclusion reached in another session is still that session's
// conclusion after it crosses a scope boundary; it does not become the current user's intent by
// being read here. Applies carries the origin scope and the evidence's own authority out in the
// result, and refuses outright to carry unsettled work — a hypothesis, a candidate extraction —
// across a boundary unless a grant said so in as many words.
//
// A refusal distinguishes "no" from "cannot tell". ReuseDenied means something positively ended
// or forbade the reuse; ReuseWithheld means the gate could not establish permission. Collapsing
// them would tell a caller to stop looking when the answer is that nobody looked yet, and would
// let a failed observation read as a decision.

// Reusability is Applies' verdict.
//
// ReuseWithheld is the zero value and both refusals block reuse, so a caller may test
// `d.Reuse == ReuseAllowed` and be right. The distinction between the two refusals is for the
// transcript and for what a caller should do next, never for whether to proceed.
type Reusability uint8

const (
	// ReuseWithheld means permission could not be established: an unobserved repository, a
	// missing or insufficient grant, an undeclared expiry, degraded dependency coverage, or a
	// lifecycle this build cannot interpret. Observing more may change the answer.
	ReuseWithheld Reusability = iota
	// ReuseAllowed means every requirement was met. It is the only value that permits reuse.
	ReuseAllowed
	// ReuseDenied means something positively forbade the reuse: a different repository, denied
	// access, or evidence that expired, was deleted, superseded or corrected. Observing more will
	// not change it; re-verifying the underlying claim is the only way forward.
	ReuseDenied
)

// String returns the human-facing spelling of r.
func (r Reusability) String() string {
	switch r {
	case ReuseWithheld:
		return "withheld"
	case ReuseAllowed:
		return "allowed"
	case ReuseDenied:
		return "denied"
	}
	return "withheld"
}

// Grant is an explicit authorization to reuse evidence beyond the session that recorded it.
//
// A grant is scoped to ONE repository and to a maximum relation. There is deliberately no way to
// spell "any repository": RepositoryID cannot be a wildcard, an unknown Repository authorizes
// nothing, and MaxRelation cannot reach RelationUnrelated because Relation.Narrower refuses it.
// Cross-repository reuse is therefore not a permission a user can grant by mistake — it is not a
// permission this type can express at all, which is what "do not share conclusions across
// unrelated repositories" has to mean if it is to survive a config file.
type Grant struct {
	// Repository is the repository this grant covers. The unknown id authorizes nothing.
	Repository RepositoryID
	// MaxRelation is the broadest relation the grant covers. A request whose relation is broader
	// is refused; a narrower one is covered.
	MaxRelation Relation
	// ExpiresAt is when the grant itself stops applying. Zero means the grant has no declared
	// expiry, which — unlike evidence, whose undeclared expiry is merely a gap — makes the grant
	// itself unusable: an authorization nobody bounded is one nobody can be shown to have
	// renewed.
	ExpiresAt core.UnixMilli
	// Authority is who granted it, in SP-20's closed vocabulary. Only AuthorityUserCorrection and
	// AuthorityExplicitDecision can authorize anything: a grant that cited a hypothesis would be
	// content promoting its own authority, which core.Authority's doc comment already forbids.
	Authority core.Authority
	// AllowUnsettled permits reuse of evidence whose own authority is AuthorityHypothesis or
	// AuthorityCandidateExtraction — another session's guesses and unfinished extractions. It is
	// off by default because §1's "cannot silently become current user intent" is exactly about
	// this evidence, and a grant that covers settled conclusions should not quietly cover guesses
	// too.
	AllowUnsettled bool
}

// Valid reports whether g could authorize anything at all, and why not when it could not.
//
// It is separate from Applies so a caller can validate a configured grant at load time rather
// than discovering at reuse time that nothing it wrote was ever going to work.
func (g Grant) Valid(now core.UnixMilli) (bool, core.Omission) {
	if g.Repository.Unknown() {
		return false, core.Omission{
			Reason:   "authorization names no observed repository",
			Recovery: "record the repository the grant is for; a grant cannot cover an unidentified one",
		}
	}
	if !g.Authority.Valid() {
		return false, core.Omission{
			Reason:   fmt.Sprintf("authorization carries no recognized authority (%q)", string(g.Authority)),
			Recovery: "re-record the grant with an explicit user decision or correction behind it",
		}
	}
	if g.Authority != core.AuthorityUserCorrection && g.Authority != core.AuthorityExplicitDecision {
		return false, core.Omission{
			Reason: fmt.Sprintf("authority %q cannot authorize reuse; only an explicit decision or a user correction can",
				string(g.Authority)),
			Recovery: "obtain an explicit decision; derived content cannot promote its own authority",
		}
	}
	if !g.MaxRelation.Narrower(RelationSameRepository) {
		return false, core.Omission{
			Reason:   fmt.Sprintf("authorization covers no reusable relation (%s)", g.MaxRelation),
			Recovery: "grant a relation between same-repository and same-session",
		}
	}
	if g.ExpiresAt == 0 {
		return false, core.Omission{
			Reason:   "authorization declares no expiry",
			Recovery: "give the grant an explicit expiry; an unbounded authorization cannot be shown to be current",
		}
	}
	if now >= g.ExpiresAt {
		return false, core.Omission{
			Reason:   fmt.Sprintf("authorization expired at %d", int64(g.ExpiresAt)),
			Recovery: "renew the grant with a fresh explicit decision",
		}
	}
	return true, core.Omission{}
}

// ReuseRequest is one question for Applies: may the evidence observed at From, in the state Life
// describes, be reused at To?
type ReuseRequest struct {
	// From is where the evidence was observed. To is where it would be reused.
	From, To ReuseScope
	// Life is what has happened to the evidence since. The zero Lifecycle means nothing has,
	// with no declared expiry.
	Life Lifecycle
	// EvidenceAuthority is the evidence's OWN authority — what kind of claim it is. An invalid or
	// missing value is treated as unsettled, because a claim whose standing nobody recorded is
	// not a settled one.
	EvidenceAuthority core.Authority
	// Grant is the authorization offered for this reuse. The zero Grant is no authorization,
	// which is what makes same-session reuse the only kind that needs none.
	Grant Grant
	// Access is the outcome of actually reaching the evidence, in SP-20's closed vocabulary. The
	// zero value ("") means no access attempt is being reported; core.OutcomeOK means one
	// succeeded. Anything else — notably core.OutcomeDenied — refuses the reuse rather than being
	// reported as absence, which is what "a retrieval error is never 'not tried'" requires.
	Access core.EvidenceOutcome
	// DependencyCoverage is the ledger's current dependency-coverage watermark
	// (Health.DependencyCoverage). A non-zero Omission means the last staleness refresh could not
	// compare dependency hashes, so freshness cannot be affirmed and cross-scope reuse is
	// withheld.
	DependencyCoverage core.Omission
	// Now is the caller's clock reading. Applies has none of its own.
	Now core.UnixMilli
}

// Applicability is Applies' full answer: the verdict, everything observation established on the
// way to it, and every gap that shaped it.
//
// Attribution and Origin are populated on EVERY result, allowed or not. A consumer that renders a
// reused claim must render them with it: that is the mechanism behind "cross-session summaries
// remain attributed derived claims", and dropping them is how another session's conclusion turns
// into this session's intent.
type Applicability struct {
	// Reuse is the verdict. Only ReuseAllowed permits reuse.
	Reuse Reusability
	// Relation is what Relate established between the two scopes.
	Relation Relation
	// Version is what comparing the two scopes' versions established. It never changes Reuse:
	// whether the code actually moved is depends_on's question.
	Version VersionAgreement
	// Disposition is what the lifecycle established.
	Disposition Disposition
	// Origin is the scope the evidence was observed in, copied verbatim from the request.
	Origin ReuseScope
	// Attribution is the authority the reused claim RETAINS. Reuse never raises it: a hypothesis
	// stays a hypothesis, and an unrecognized authority stays AuthorityHypothesis rather than
	// being upgraded to the caller's own standing.
	Attribution core.Authority
	// Omissions lists every gap that shaped the verdict, in the order the gate found them. It is
	// empty only for an unqualified ReuseAllowed.
	Omissions []core.Omission
}

// Allowed reports whether a permits reuse. It is the only test a caller needs; both refusals
// block.
func (a Applicability) Allowed() bool { return a.Reuse == ReuseAllowed }

// settled reports whether an evidence authority is a settled conclusion rather than another
// session's guess or unfinished extraction. An unrecognized authority is NOT settled: a claim
// whose standing this build cannot read is not one it may adopt.
func settled(a core.Authority) bool {
	switch a {
	case core.AuthorityUserCorrection, core.AuthorityExplicitDecision, core.AuthorityToolObservation:
		return true
	default:
		return false
	}
}

// retainedAuthority is the authority a reused claim keeps. A recognized authority is kept as it
// is; anything else becomes AuthorityHypothesis, which is the weakest standing the closed
// vocabulary has and therefore the only safe reading of a value this build does not know.
func retainedAuthority(a core.Authority) core.Authority {
	if a.Valid() {
		return a
	}
	return core.AuthorityHypothesis
}

// Applies decides whether req's evidence may be reused, and reports everything that shaped the
// decision.
//
// The gate runs in this order, and the order is the specification:
//
//  1. ACCESS. A reported access outcome other than success denies the reuse. This runs first
//     because a denial or a fault is a fact about the attempt, not about the evidence, and must
//     never be reported downstream as absence.
//  2. LIFECYCLE. Expired, deleted, superseded and corrected evidence is denied; an
//     uninterpretable lifecycle is withheld. Nothing below can rehabilitate it.
//  3. RELATION. An unobserved repository withholds and names which fields are missing; a
//     different repository denies. Same-session reuse is allowed here and needs no grant, because
//     evidence that never left the session it was recorded in is not crossing anything.
//  4. AUTHORIZATION. Every broader relation needs a valid grant, for THIS repository, covering
//     this relation. Unsettled evidence additionally needs the grant to say so.
//  5. COVERAGE. An undeclared evidence expiry, or a degraded dependency-coverage watermark,
//     withholds cross-scope reuse: §1 requires reuse to be qualified by an explicit expiry, and
//     freshness that the last refresh could not confirm is not freshness.
//
// Steps 4 and 5 accumulate every failure rather than stopping at the first, so one transcript
// tells a caller everything it would have to fix. Steps 1 to 3 return immediately: a denial is
// final, and reporting further gaps behind it would imply they were the obstacle.
func Applies(req ReuseRequest) Applicability {
	out := Applicability{
		Origin:      req.From,
		Attribution: retainedAuthority(req.EvidenceAuthority),
		Relation:    Relate(req.From, req.To),
		Version:     Versions(req.From, req.To),
		Disposition: req.Life.Disposition(req.Now),
	}

	// 1. Access.
	if req.Access != "" && req.Access != core.OutcomeOK {
		out.Reuse = ReuseDenied
		out.Omissions = append(out.Omissions, core.Omission{
			Reason:   fmt.Sprintf("evidence access reported %q", string(req.Access)),
			Recovery: "resolve the access outcome; a failed read is not an absent elimination",
		})
		return out
	}

	// 2. Lifecycle.
	if !out.Disposition.Live() {
		if out.Disposition == DispositionUncertain {
			out.Reuse = ReuseWithheld
		} else {
			out.Reuse = ReuseDenied
		}
		out.Omissions = append(out.Omissions, out.Disposition.omission(req.Life))
		return out
	}

	// 3. Relation.
	switch out.Relation {
	case RelationUnknown:
		out.Reuse = ReuseWithheld
		out.Omissions = append(out.Omissions, unobservedOmission(req))
		return out
	case RelationUnrelated:
		out.Reuse = ReuseDenied
		out.Omissions = append(out.Omissions, core.Omission{
			Reason:   "evidence was observed in a different repository",
			Recovery: "re-verify the approach here; conclusions do not cross repositories",
		})
		return out
	case RelationSameSession:
		out.Reuse = ReuseAllowed
		return out
	}

	// 4. Authorization.
	if ok, why := req.Grant.Valid(req.Now); !ok {
		out.Reuse = ReuseWithheld
		out.Omissions = append(out.Omissions, why)
	} else {
		if req.Grant.Repository != req.To.Repository {
			out.Reuse = ReuseWithheld
			out.Omissions = append(out.Omissions, core.Omission{
				Reason:   "authorization is for a different repository than the one being worked in",
				Recovery: "obtain a grant for this repository",
			})
		}
		if !out.Relation.Narrower(req.Grant.MaxRelation) {
			out.Reuse = ReuseWithheld
			out.Omissions = append(out.Omissions, core.Omission{
				Reason: fmt.Sprintf("relation %s is broader than the authorized %s",
					out.Relation, req.Grant.MaxRelation),
				Recovery: "broaden the grant explicitly, or reuse only within the authorized relation",
			})
		}
		if !settled(req.EvidenceAuthority) && !req.Grant.AllowUnsettled {
			out.Reuse = ReuseWithheld
			out.Omissions = append(out.Omissions, core.Omission{
				Reason: fmt.Sprintf("evidence is unsettled (%s) and the grant does not cover unsettled work",
					string(out.Attribution)),
				Recovery: "another session's hypotheses and unfinished tasks need an explicit grant that says so",
			})
		}
	}

	// 5. Coverage.
	if !req.Life.ExpiryDeclared() {
		out.Reuse = ReuseWithheld
		out.Omissions = append(out.Omissions, core.Omission{
			Reason:   "evidence declares no expiry",
			Recovery: "record an explicit expiry before reusing this evidence outside its session",
		})
	}
	if req.DependencyCoverage != (core.Omission{}) {
		out.Reuse = ReuseWithheld
		out.Omissions = append(out.Omissions, req.DependencyCoverage)
	}

	if len(out.Omissions) == 0 {
		out.Reuse = ReuseAllowed
	}
	return out
}

// unobservedOmission explains a RelationUnknown by naming which scope fields were not observed,
// on whichever side is missing them. It reports the ORIGIN's gaps in preference to the target's,
// because the origin is the side a caller can no longer go back and re-observe.
func unobservedOmission(req ReuseRequest) core.Omission {
	side, missing := "evidence", req.From.Unobserved()
	if !req.From.Repository.Unknown() {
		side, missing = "current", req.To.Unobserved()
	}
	return core.Omission{
		Reason:   fmt.Sprintf("%s scope is unobserved (%v)", side, missing),
		Recovery: "observe the repository identity before reusing across scopes; blank fields are not a match",
	}
}

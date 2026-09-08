// Package admission is SP-21's opt-in deterministic admission control: the M4 pipeline that may
// replace one newly delivered result with a capsule or a pointer, once the delivered content is
// durably captured and recoverable.
//
// It ships disabled. `runtime.migration.replacement.newResult` is refused until the M4 gate passes,
// and the zero value of every type here is the refusing one, so a caller that never reads
// configuration gets pass-through rather than replacement.
//
// # What this file is
//
// This is the frozen policy surface, authored before the pipeline that feeds it. Decide is a pure
// function of the gate state and the first failing stage, deliberately: SP-21's invariants are
// properties of the decision, not of the I/O around it, and freezing them here lets the capture,
// representation and recovery slices be authored against a contract that cannot drift underneath
// them. Nothing in this file performs capture, transformation, resolution or delivery.
//
// # Architecture
//
// internal/admission is foundation-only under 00-ARCHITECTURE §3.2. That is a commitment rather
// than a stage: the pipeline reaches SP-20 capture and publication, and SP-13 handle resolution,
// through ports declared in this package and satisfied at a composition root. A slice that needs a
// real internal/ import is an amendment to §3.2, not a local decision.
package admission

import "github.com/qompack/qompack/internal/core"

// Outcome is what admission did with a delivered result.
//
// OutcomePassThrough is the zero value on purpose. A Record nobody populated reports that the
// original result was delivered untouched, which is the answer that cannot cause harm; if
// OutcomeTransform were zero, every unfilled Record would claim a replacement happened.
type Outcome int

const (
	// OutcomePassThrough delivers the exact original result. Every failure that is not a privacy
	// denial ends here (SP-21 invariant 10).
	OutcomePassThrough Outcome = iota

	// OutcomeTransform delivers one transformed representation. Reached only when the gate admits
	// the target and no stage failed.
	OutcomeTransform

	// OutcomeDeny withholds delivery under privacy policy. It is never an optimization fallback.
	OutcomeDeny
)

// String renders an Outcome for admission records and failure messages.
func (o Outcome) String() string {
	switch o {
	case OutcomePassThrough:
		return "pass-through"
	case OutcomeTransform:
		return "transform"
	case OutcomeDeny:
		return "deny"
	default:
		return "unknown"
	}
}

// Reason is why an Outcome was chosen. It is recorded verbatim in the admission record, because
// "passed through" without a reason cannot be audited: a disabled switch, an unknown schema and a
// failed capture are three different operational facts with the same outcome.
type Reason int

const (
	// ReasonDisabled is the shipped state: the feature switch is off.
	ReasonDisabled Reason = iota

	// ReasonUnknownTarget covers a schema or version outside the allowlist, and a result that is
	// not Qompack-owned. Dispatch is exact; anything unrecognized passes through.
	ReasonUnknownTarget

	// ReasonCaptureFailed means durable capture did not succeed, so replacement is forbidden
	// (SP-21 invariant 2).
	ReasonCaptureFailed

	// ReasonPublicationUnverified means capture succeeded but its publication was not verified.
	ReasonPublicationUnverified

	// ReasonParseFailed means the payload did not parse under its declared schema and version.
	ReasonParseFailed

	// ReasonNoRepresentation means no compatible representation was selected.
	ReasonNoRepresentation

	// ReasonHandleUnresolvable means an emitted handle did not resolve under current
	// authorization, which blocks the transform (SP-21 invariant 6).
	ReasonHandleUnresolvable

	// ReasonPolicyUnavailable means the privacy check could not be evaluated — an unwired port,
	// or a policy that errored. It is NOT a denial. "Policy said no" and "we could not ask policy"
	// are different facts with different safe answers: a denial withholds the delivered result
	// from the user, and only an actual policy decision may do that. An unanswerable check
	// passes the original through and captures nothing.
	ReasonPolicyUnavailable

	// ReasonPrivacyDenied is the only reason that produces OutcomeDeny.
	ReasonPrivacyDenied

	// ReasonAdmitted is the single success reason.
	ReasonAdmitted
)

// String renders a Reason for admission records and failure messages.
func (r Reason) String() string {
	switch r {
	case ReasonDisabled:
		return "disabled"
	case ReasonUnknownTarget:
		return "unknown-target"
	case ReasonCaptureFailed:
		return "capture-failed"
	case ReasonPublicationUnverified:
		return "publication-unverified"
	case ReasonParseFailed:
		return "parse-failed"
	case ReasonNoRepresentation:
		return "no-representation"
	case ReasonHandleUnresolvable:
		return "handle-unresolvable"
	case ReasonPolicyUnavailable:
		return "policy-unavailable"
	case ReasonPrivacyDenied:
		return "privacy-denied"
	case ReasonAdmitted:
		return "admitted"
	default:
		return "unknown"
	}
}

// Stage names a pipeline step. StageNone is the zero value: no stage failed.
type Stage int

const (
	StageNone Stage = iota
	StageCapture
	StagePublication
	StageParse
	StageSelection
	StageResolution

	// StagePolicy is the privacy CHECK — asking. It fails when policy cannot be evaluated, which
	// is an ordinary stage failure and passes through.
	StagePolicy

	// StagePrivacy is not an ordinary stage. A failure at StagePrivacy is a policy decision to
	// withhold, and it is the only input that produces OutcomeDeny.
	StagePrivacy
)

// String renders a Stage for admission records and failure messages.
func (s Stage) String() string {
	switch s {
	case StageNone:
		return "none"
	case StageCapture:
		return "capture"
	case StagePublication:
		return "publication"
	case StageParse:
		return "parse"
	case StageSelection:
		return "selection"
	case StageResolution:
		return "resolution"
	case StagePolicy:
		return "policy"
	case StagePrivacy:
		return "privacy"
	default:
		return "unknown"
	}
}

// Target is the schema and version a delivered result declares.
//
// Both halves are part of the identity. A host that keeps its schema name and changes its version
// is emitting a different output shape, and matching on the name alone is how a parser gets applied
// to a payload no canary ever covered.
//
// A Target is untrusted input. It says what the producer claims, never what was verified.
type Target struct {
	Schema  string
	Version string
}

// Allowlist is the exact set of host targets admission may parse.
//
// The shipped allowlist is empty and stays empty until B01 target evidence exists: SP-21 admits a
// host schema only after that exact schema and version produce an installed-host canary transcript
// and a competing-hook observation. Qompack-owned results do not go through the allowlist; they are
// admitted by Gate.Owned, which is the first supported surface.
type Allowlist struct {
	// allowed is nil for the empty allowlist, which is the shipped state. A nil map reads as
	// absent for every key, so Permits needs no special case.
	allowed map[Target]struct{}
}

// NewAllowlist returns an allowlist permitting exactly the given targets. Called with none, it
// returns the empty allowlist the product ships.
func NewAllowlist(targets ...Target) Allowlist {
	if len(targets) == 0 {
		return Allowlist{}
	}
	allowed := make(map[Target]struct{}, len(targets))
	for _, t := range targets {
		allowed[t] = struct{}{}
	}
	return Allowlist{allowed: allowed}
}

// Permits reports whether t matches an allowlist entry exactly, on both schema and version.
func (a Allowlist) Permits(t Target) bool {
	_, ok := a.allowed[t]
	return ok
}

// Empty reports whether the allowlist permits nothing.
func (a Allowlist) Empty() bool { return len(a.allowed) == 0 }

// Gate is the admission decision's configuration input.
//
// The zero Gate is disabled, unowned and empty-allowlisted, which refuses everything.
type Gate struct {
	// Enabled mirrors runtime.migration.replacement.newResult. False is the shipped value and the
	// zero value.
	Enabled bool

	// Owned marks a Qompack-owned result — the first and currently only supported surface.
	Owned bool

	// Allow is the host target allowlist, empty until target evidence exists.
	Allow Allowlist
}

// Admits reports whether the gate permits t to be considered for transformation, and why not when
// it does not.
func (g Gate) Admits(t Target) (bool, Reason) {
	if !g.Enabled {
		return false, ReasonDisabled
	}
	if g.Owned || g.Allow.Permits(t) {
		return true, ReasonAdmitted
	}
	return false, ReasonUnknownTarget
}

// Failure is the first stage that failed, if any. Its zero value means no stage failed.
type Failure struct {
	Stage Stage
	Err   error
}

// Record is the admission record: what was decided about one delivered result, and why.
type Record struct {
	Outcome Outcome
	Reason  Reason
	Target  Target

	// Stage is the stage that failed, or StageNone.
	Stage Stage

	// Err is the underlying stage error, retained for the audit diagnostic. It is never used to
	// choose an outcome — the stage is.
	Err error

	// Handle is the resolvable pointer the capture produced, and is set only on an admitted
	// record. Every refusal leaves it empty, which is what makes "a failed capture emitted no
	// handle" checkable rather than merely intended.
	Handle string

	// Fidelity is the admitted capture's recoverability, carried into the record so an audit can
	// see WHY a replacement was allowed and not only that it was.
	Fidelity Fidelity

	// Coverage is where the delivered content ended up. An admitted representation leaves the
	// original bytes in the archive; every refusal reports CoverageUnknown, because admission
	// does not observe what the host then does with a result it passed through, and an
	// unobserved location is unknown rather than blank.
	Coverage core.Coverage

	// Meaning is the preserved structured and displayed meaning, set only on an admitted record.
	// A refusal leaves it zero: a caller must not be handed something that looks actionable
	// after the pipeline declined to act.
	Meaning Meaning

	// Form is the representation shape selected. FormNone on every refusal.
	Form Form

	// Base names the baseline a delta is relative to, and is empty for a capsule.
	Base string

	// HandleState is what resolving the handle found, and it is set on a blocked record as well
	// as an admitted one. Every non-resolvable answer produces the same outcome and the same
	// reason, so this is the only field that says whether to grant a permission, restore an
	// object, or go look at the resolver.
	HandleState HandleState

	// Reset is why a delta was refused in favour of a capsule. It is the baseline-verification
	// record: every reset produces the same form, so the cause is the only part that is
	// diagnostic.
	Reset ResetCause
}

// Decide is SP-21's frozen admission policy.
//
// The order below is the contract, and two orderings in it are load-bearing:
//
// Privacy is evaluated FIRST, before the feature switch. Qompack.md §7.1: "Privacy-denied data
// follows privacy policy even when optimization otherwise fails toward pass-through." The tempting
// implementation returns early on a disabled switch, which would turn a denial into a delivery of
// the very bytes policy refused. The kill switch disables transformation, not privacy.
//
// The gate is then evaluated BEFORE stage failures, so that a disabled build reports "disabled"
// rather than blaming a stage that never ran. Both answers are pass-through; the record differs,
// and a deterministic record is the point of an audit trail.
func Decide(g Gate, t Target, f Failure) Record {
	rec := Record{Target: t, Stage: f.Stage, Err: f.Err, Coverage: core.CoverageUnknown}

	if f.Stage == StagePrivacy {
		rec.Outcome, rec.Reason = OutcomeDeny, ReasonPrivacyDenied
		return rec
	}

	if ok, why := g.Admits(t); !ok {
		rec.Outcome, rec.Reason = OutcomePassThrough, why
		// No stage ran, so the record must not name one.
		rec.Stage = StageNone
		return rec
	}

	if f.Stage != StageNone {
		rec.Outcome, rec.Reason = OutcomePassThrough, reasonForStage(f.Stage)
		return rec
	}

	rec.Outcome, rec.Reason = OutcomeTransform, ReasonAdmitted
	return rec
}

// reasonForStage maps a failed stage to its recorded reason.
//
// StagePrivacy is absent because Decide handles it before reaching here, and StageNone is absent
// because it is not a failure. An unrecognized stage falls to ReasonNoRepresentation, which is a
// pass-through reason: a stage this policy does not know about must not be able to admit anything.
func reasonForStage(s Stage) Reason {
	switch s {
	case StageCapture:
		return ReasonCaptureFailed
	case StagePublication:
		return ReasonPublicationUnverified
	case StageParse:
		return ReasonParseFailed
	case StageResolution:
		return ReasonHandleUnresolvable
	case StagePolicy:
		return ReasonPolicyUnavailable
	default:
		return ReasonNoRepresentation
	}
}

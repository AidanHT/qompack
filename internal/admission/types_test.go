package admission_test

import (
	"errors"
	"testing"

	"github.com/qompack/qompack/internal/admission"
	"github.com/stretchr/testify/require"
)

// These are SP-21's commit 1 contracts: T21-SWITCH-01, T21-PASS-01 and T21-HOST-01, authored
// against the frozen surface before any pipeline exists.
//
// What is deliberately NOT here: capture, publication, transformation and handle resolution. Those
// are commits 2 through 4 and they need SP-20 and SP-13 seams. This slice freezes the POLICY — the
// decision every later stage feeds into — so the stages can be authored against a contract that
// cannot drift while they are written. admission.Decide is a pure function for that reason: the
// three invariants below are properties of the policy, not of any I/O the pipeline later performs.
//
// The allowlist stays empty in every test here, because it is empty in the product. B01 keeps the
// installed host unverified, and SP-21 admits a host schema only after that exact schema and
// version produce a target canary transcript. A test that populated it would be asserting against
// evidence nobody has.

// tgt is the shape a delivered result declares. Nothing in this package treats it as trusted.
var tgt = admission.Target{Schema: "qompack.retrieval", Version: "1"}

// TestSwitchIsOffByDefault is T21-SWITCH-01's first clause, and invariant 1.
//
// The zero Gate is the disabled Gate. That is not decoration: a caller that forgets to consult
// configuration, or reads a config block that does not exist yet, gets refusal rather than
// admission. Every other default in this package is chosen the same way.
func TestSwitchIsOffByDefault(t *testing.T) {
	var zero admission.Gate

	rec := admission.Decide(zero, tgt, admission.Failure{})

	require.Equal(t, admission.OutcomePassThrough, rec.Outcome,
		"the zero Gate must refuse; a forgotten configuration read cannot enable replacement")
	require.Equal(t, admission.ReasonDisabled, rec.Reason)
}

// TestPassThroughIsTheZeroOutcome pins the enum ordering itself.
//
// OutcomePassThrough is iota, so a Record nobody filled in reports the safe answer. Reordering
// these so that OutcomeTransform became the zero value would make every unpopulated Record claim a
// transformation happened, which is invariant 2 inverted.
func TestPassThroughIsTheZeroOutcome(t *testing.T) {
	var rec admission.Record

	require.Equal(t, admission.OutcomePassThrough, rec.Outcome,
		"the zero Record must report pass-through, never a transformation")
}

// TestExplicitOptInAdmitsAnOwnedResult is T21-SWITCH-01's second clause: with the switch on and an
// owned result, and no stage failing, the policy admits exactly one transformation.
func TestExplicitOptInAdmitsAnOwnedResult(t *testing.T) {
	g := admission.Gate{Enabled: true, Owned: true}

	rec := admission.Decide(g, tgt, admission.Failure{})

	require.Equal(t, admission.OutcomeTransform, rec.Outcome)
	require.Equal(t, admission.ReasonAdmitted, rec.Reason)
	require.Equal(t, tgt, rec.Target, "the record must name the target it decided about")
}

// TestUnknownSchemaPassesThrough is T21-HOST-01: dispatch requires an exact match, and the
// allowlist is empty, so every host schema passes through.
//
// Both halves of Target are checked because "exact" means both. A host that kept its schema name
// and changed its version is a different output shape, and matching on name alone is how a parser
// gets applied to a payload it was never tested against.
func TestUnknownSchemaPassesThrough(t *testing.T) {
	g := admission.Gate{Enabled: true, Owned: false, Allow: admission.NewAllowlist()}
	require.True(t, g.Allow.Empty(), "the shipped allowlist is empty until B01 target evidence exists")

	for _, target := range []admission.Target{
		{Schema: "some.host.tool_result", Version: "1"},
		{Schema: "", Version: ""},
		tgt, // even Qompack's own schema, when the result is not owned
	} {
		rec := admission.Decide(g, target, admission.Failure{})

		require.Equal(t, admission.OutcomePassThrough, rec.Outcome,
			"schema %q version %q is not on the allowlist and must pass through",
			target.Schema, target.Version)
		require.Equal(t, admission.ReasonUnknownTarget, rec.Reason)
	}
}

// TestAllowlistRequiresBothSchemaAndVersion is the exactness rule stated directly against the
// allowlist, independent of Decide.
func TestAllowlistRequiresBothSchemaAndVersion(t *testing.T) {
	allow := admission.NewAllowlist(admission.Target{Schema: "host.result", Version: "2"})

	require.True(t, allow.Permits(admission.Target{Schema: "host.result", Version: "2"}))
	require.False(t, allow.Permits(admission.Target{Schema: "host.result", Version: "3"}),
		"a version the canary never covered is a different output shape")
	require.False(t, allow.Permits(admission.Target{Schema: "host.Result", Version: "2"}),
		"schema matching is exact, not case-folded")
	require.False(t, allow.Empty(), "a populated allowlist must not report itself empty")
}

// TestEveryStageFailurePassesTheOriginalThrough is T21-PASS-01, and invariant 10.
//
// Each stage is asserted separately rather than as a table with one representative, because the
// failure modes reach this policy from different owners — capture and publication from SP-20,
// resolution from SP-13, parse and selection from this package — and a later edit that made one of
// them transform anyway is exactly the defect this row exists to catch.
func TestEveryStageFailurePassesTheOriginalThrough(t *testing.T) {
	g := admission.Gate{Enabled: true, Owned: true}
	boom := errors.New("stage failed")

	for stage, want := range map[admission.Stage]admission.Reason{
		admission.StageCapture:     admission.ReasonCaptureFailed,
		admission.StagePublication: admission.ReasonPublicationUnverified,
		admission.StageParse:       admission.ReasonParseFailed,
		admission.StageSelection:   admission.ReasonNoRepresentation,
		admission.StageResolution:  admission.ReasonHandleUnresolvable,
	} {
		rec := admission.Decide(g, tgt, admission.Failure{Stage: stage, Err: boom})

		require.Equal(t, admission.OutcomePassThrough, rec.Outcome,
			"a %v failure must deliver the original result unchanged", stage)
		require.Equal(t, want, rec.Reason)
		require.Equal(t, stage, rec.Stage, "the record must name the stage that failed")
	}
}

// TestPrivacyDenialIsNotPassThrough is T21-PASS-01's exception, and the second half of invariant 10.
//
// Denial is a different outcome from pass-through and must never be reported as one. A denial that
// degraded into pass-through would deliver the very bytes privacy policy refused.
func TestPrivacyDenialIsNotPassThrough(t *testing.T) {
	g := admission.Gate{Enabled: true, Owned: true}

	rec := admission.Decide(g, tgt, admission.Failure{Stage: admission.StagePrivacy})

	require.Equal(t, admission.OutcomeDeny, rec.Outcome)
	require.Equal(t, admission.ReasonPrivacyDenied, rec.Reason)
}

// TestPrivacyDenialSurvivesTheSwitchBeingOff is the rule Qompack.md §7.1 states and the one most
// likely to be got wrong: "Privacy-denied data follows privacy policy even when optimization
// otherwise fails toward pass-through."
//
// The tempting implementation checks the feature switch first and returns early. That is wrong.
// Privacy policy is not part of the optimization, so turning admission off must not turn a denial
// into a delivery — the kill switch disables transformation, not privacy.
func TestPrivacyDenialSurvivesTheSwitchBeingOff(t *testing.T) {
	var off admission.Gate

	rec := admission.Decide(off, tgt, admission.Failure{Stage: admission.StagePrivacy})

	require.Equal(t, admission.OutcomeDeny, rec.Outcome,
		"disabling admission must not convert a privacy denial into a delivery")
	require.Equal(t, admission.ReasonPrivacyDenied, rec.Reason)
}

// TestDisabledGateOutranksAStageFailure fixes the reported reason when more than one thing is
// wrong, so the admission record is deterministic.
//
// Both answers are pass-through, so the outcome is not in question; what matters is that the
// record says the switch was off rather than blaming a stage that never should have run.
func TestDisabledGateOutranksAStageFailure(t *testing.T) {
	var off admission.Gate

	rec := admission.Decide(off, tgt, admission.Failure{Stage: admission.StageCapture})

	require.Equal(t, admission.OutcomePassThrough, rec.Outcome)
	require.Equal(t, admission.ReasonDisabled, rec.Reason,
		"a disabled gate is the reason; no stage ran to fail")
}

// TestEveryEnumValueRendersADistinctName covers the String methods, and exists because of how they
// fail: an enum gains a value, its String arm is forgotten, and the admission record starts
// reporting "unknown" for a real outcome. A reader of that record cannot tell a missing arm from a
// genuinely unrecognized value, so the audit trail degrades silently.
//
// assertEnumIsExhaustive is what makes it more than a hand-kept list. Listing values by hand was
// the original shape, and commit 5 caught it: two reasons were added, both got String arms, neither
// got a list entry, and the test went on passing — the coverage floor was the only thing that
// noticed. Now a value added without a list entry fails here directly.
func TestEveryEnumValueRendersADistinctName(t *testing.T) {
	t.Run("outcome", func(t *testing.T) {
		assertEnumIsExhaustive(t, "unknown", []string{
			admission.OutcomePassThrough.String(),
			admission.OutcomeTransform.String(),
			admission.OutcomeDeny.String(),
		}, func(i int) string { return admission.Outcome(i).String() })
	})

	t.Run("reason", func(t *testing.T) {
		assertEnumIsExhaustive(t, "unknown", []string{
			admission.ReasonDisabled.String(),
			admission.ReasonUnknownTarget.String(),
			admission.ReasonCaptureFailed.String(),
			admission.ReasonPublicationUnverified.String(),
			admission.ReasonParseFailed.String(),
			admission.ReasonNoRepresentation.String(),
			admission.ReasonHandleUnresolvable.String(),
			admission.ReasonPolicyUnavailable.String(),
			admission.ReasonPrivacyDenied.String(),
			admission.ReasonAlreadyProcessed.String(),
			admission.ReasonForeignTransform.String(),
			admission.ReasonAdmitted.String(),
		}, func(i int) string { return admission.Reason(i).String() })
	})

	t.Run("stage", func(t *testing.T) {
		assertEnumIsExhaustive(t, "unknown", []string{
			admission.StageNone.String(),
			admission.StageCapture.String(),
			admission.StagePublication.String(),
			admission.StageParse.String(),
			admission.StageSelection.String(),
			admission.StageResolution.String(),
			admission.StagePolicy.String(),
			admission.StagePrivacy.String(),
		}, func(i int) string { return admission.Stage(i).String() })
	})
}

// assertEnumIsExhaustive checks that names covers every value the enum actually has.
//
// It walks upward from zero until render returns sentinel — the string an unmapped value renders —
// which works because every enum in this package is a contiguous iota run from zero. Two different
// mistakes fail it. A value added without a String arm stops the walk early, so the counts
// disagree. A value added WITH an arm but not listed by the caller also makes the counts disagree.
// Neither was catchable while the list was maintained by hand.
func assertEnumIsExhaustive(t *testing.T, sentinel string, names []string, render func(int) string) {
	t.Helper()

	seen := map[string]bool{}
	for i, name := range names {
		require.NotEqual(t, sentinel, name, "value %d has no String arm", i)
		require.False(t, seen[name], "%q is rendered by two values", name)
		seen[name] = true
	}

	const walkLimit = 1000
	n := 0
	for render(n) != sentinel {
		n++
		require.Less(t, n, walkLimit, "the enum walk did not reach an unmapped value")
	}

	require.Equal(t, n, len(names),
		"the enum has %d mapped values but %d are covered here; a value was added without a case",
		n, len(names))
	require.Equal(t, sentinel, render(n+1), "values past the end must render the sentinel")
}

// TestUnrecognizedStageCannotAdmit pins reasonForStage's default arm.
//
// A stage this policy does not know about must fall to a pass-through reason. The dangerous
// alternative is a default that reaches the success path, which would let an unmapped stage admit a
// transformation nobody specified.
func TestUnrecognizedStageCannotAdmit(t *testing.T) {
	g := admission.Gate{Enabled: true, Owned: true}

	rec := admission.Decide(g, tgt, admission.Failure{Stage: admission.Stage(99)})

	require.Equal(t, admission.OutcomePassThrough, rec.Outcome,
		"an unmapped stage must never reach the transform path")
	require.Equal(t, admission.ReasonNoRepresentation, rec.Reason)
}

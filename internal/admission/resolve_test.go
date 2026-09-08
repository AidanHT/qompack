package admission_test

import (
	"context"
	"errors"
	"testing"

	"github.com/qompack/qompack/internal/admission"
	"github.com/stretchr/testify/require"
)

// SP-21 commit 4's contracts: T21-RECOVERY-01 and invariant 6 — "every emitted handle resolves
// under current authorization before delivery. An unresolvable, denied or unavailable handle blocks
// the transform and stays visible as such."
//
// The plan's serial edge puts this commit after the M2/SP-13 authorization and recovery gate, and
// the seam here is a port for the same §3.2 reason capture and publication are: internal/admission
// is foundation-only, so it reaches SP-13 through an interface a composition root satisfies. What
// these tests grade is which resolution answers may admit — which is the whole of invariant 6 that
// belongs to this package. The adapter that binds Resolver to real SP-13 authorization is a
// composition-root task, and until it exists T21-RECOVERY-01's "against real SP-13/M2
// authorization" artifact is not satisfied by this commit alone.

// TestResolutionRunsLast fixes the position of the new stage: the handle is resolved after the
// representation that will carry it has been selected, and before anything is delivered.
func TestResolutionRunsLast(t *testing.T) {
	r := newRecorder()
	p := admission.NewPipeline(admitting, r.ports())

	rec, err := p.Admit(context.Background(), delivery)

	require.NoError(t, err)
	require.Equal(t, []string{"privacy", "capture", "publish", "parse", "resolve"}, r.calls,
		"the handle is resolved once, at the end, on the representation that will carry it")
	require.Equal(t, admission.OutcomeTransform, rec.Outcome)
	require.Equal(t, admission.HandleResolvable, rec.HandleState)
}

// TestTheResolverIsAskedForTheCapturesOwnHandle is the assertion that keeps the check honest.
//
// Resolving some other handle — a baseline's, or a handle built from the target — would report
// success for something nobody is about to deliver. The handle under test must be the one the
// capture produced and the one the record will carry.
func TestTheResolverIsAskedForTheCapturesOwnHandle(t *testing.T) {
	r := newRecorder()
	p := admission.NewPipeline(admitting, r.ports())

	rec, err := p.Admit(context.Background(), delivery)

	require.NoError(t, err)
	require.Equal(t, []string{r.handle}, r.resolved,
		"exactly the capture's handle is resolved, exactly once")
	require.Equal(t, r.handle, rec.Handle)
}

// TestEveryNonResolvableStateBlocksTheTransform is invariant 6's first half, and the sweep matters:
// these answers arrive from different parts of SP-13, and a later edit that let one of them through
// is the defect this catches.
//
// The second half is that each stays VISIBLE as itself. Every row here produces the same outcome
// and the same reason, so the outcome cannot tell an operator whether authorization refused them,
// the object is gone, or the resolver could not say — three different facts with three different
// fixes. HandleState is what keeps them apart.
func TestEveryNonResolvableStateBlocksTheTransform(t *testing.T) {
	for name, state := range map[string]admission.HandleState{
		"denied":      admission.HandleDenied,
		"unavailable": admission.HandleUnavailable,
		"uncertain":   admission.HandleUncertain,
		"unknown":     admission.HandleUnknown,
	} {
		t.Run(name, func(t *testing.T) {
			r := newRecorder()
			r.handleState = state
			p := admission.NewPipeline(admitting, r.ports())

			rec, err := p.Admit(context.Background(), delivery)

			require.NoError(t, err)
			require.Equal(t, admission.OutcomePassThrough, rec.Outcome)
			require.Equal(t, admission.ReasonHandleUnresolvable, rec.Reason)
			require.Equal(t, admission.StageResolution, rec.Stage)
			require.Equal(t, admission.FormNone, rec.Form, "a blocked transform emits no form")
			require.Empty(t, rec.Handle, "an unresolvable handle is not emitted")
			require.Equal(t, state, rec.HandleState,
				"the resolution answer stays visible as itself, not folded into 'unresolvable'")
			require.False(t, state.Resolvable())
		})
	}
}

// TestADeniedHandleIsNotAPrivacyDenial is the sharpest distinction in this commit, and the same
// mistake the privacy port taught: two different facts, two different safe answers.
//
// A denied HANDLE means this reader may not read the archived copy. The delivered result is not
// the archived copy — it is the user's own tool output, which they were always entitled to. So the
// answer is to deliver it untouched. Returning OutcomeDeny here would withhold a result on the
// strength of an authorization decision about a DIFFERENT object, which is a privacy denial the
// privacy policy never made.
func TestADeniedHandleIsNotAPrivacyDenial(t *testing.T) {
	r := newRecorder()
	r.handleState = admission.HandleDenied
	p := admission.NewPipeline(admitting, r.ports())

	rec, err := p.Admit(context.Background(), delivery)

	require.NoError(t, err)
	require.Equal(t, admission.OutcomePassThrough, rec.Outcome,
		"authorization over the archived copy must not withhold the user's own delivered result")
	require.NotEqual(t, admission.OutcomeDeny, rec.Outcome)
	require.NotEqual(t, admission.ReasonPrivacyDenied, rec.Reason,
		"ReasonPrivacyDenied is reserved for a privacy-policy decision about this delivery")
}

// TestUncertainIsNotResolvable states the rule the enum's zero value already implies, because it is
// the one a resolver author is most likely to get wrong.
//
// "We could not determine whether this resolves" is not "it resolves". Absent evidence is not
// evidence, and the pipeline that admitted on uncertainty would emit a handle that may already be
// gone.
func TestUncertainIsNotResolvable(t *testing.T) {
	require.True(t, admission.HandleResolvable.Resolvable())

	for _, s := range []admission.HandleState{
		admission.HandleUnknown, admission.HandleDenied,
		admission.HandleUnavailable, admission.HandleUncertain,
	} {
		require.False(t, s.Resolvable(), "%v must not read as resolvable", s)
	}
}

// TestResolverErrorIsUncertainNotDenied fixes the answer when the resolver itself fails.
//
// A transport failure is not an authorization decision. Recording it as denied would tell an
// operator to go fix permissions for a store that was merely unreachable.
func TestResolverErrorIsUncertainNotDenied(t *testing.T) {
	r := newRecorder()
	r.resolveErr = errors.New("daemon unreachable")
	p := admission.NewPipeline(admitting, r.ports())

	rec, err := p.Admit(context.Background(), delivery)

	require.NoError(t, err)
	require.Equal(t, admission.OutcomePassThrough, rec.Outcome)
	require.Equal(t, admission.HandleUncertain, rec.HandleState,
		"a resolver that failed did not decide anything about authorization")
	require.ErrorContains(t, rec.Err, "daemon unreachable")
}

// TestNilResolverPortRefusesRatherThanPanicking is the composition root that wired everything but
// recovery — reachable only once capture, publication and parsing are wired, which is why it needs
// its own case rather than falling out of the all-nil test.
func TestNilResolverPortRefusesRatherThanPanicking(t *testing.T) {
	r := newRecorder()
	p := admission.NewPipeline(admitting, admission.Ports{
		Privacy: r, Capture: r, Publish: r, Parse: r,
	})

	rec, err := p.Admit(context.Background(), delivery)

	require.NoError(t, err)
	require.Equal(t, admission.OutcomePassThrough, rec.Outcome)
	require.Equal(t, admission.ReasonHandleUnresolvable, rec.Reason)
	require.Equal(t, admission.HandleUnknown, rec.HandleState,
		"an absent resolver resolved nothing, which is unknown rather than denied")
	require.ErrorIs(t, rec.Err, admission.ErrPortsUnwired)
}

// TestARefusedSelectionNeverReachesTheResolver is the ordering guard from the failure side.
//
// Resolving a handle for a representation that was never selected is work spent on a delivery
// already destined to pass through, and it puts an authorization question to SP-13 that nothing
// will act on.
func TestARefusedSelectionNeverReachesTheResolver(t *testing.T) {
	r := newRecorder()
	r.meaning.Media = true
	p := admission.NewPipeline(admitting, r.ports())

	_, err := p.Admit(context.Background(), delivery)

	require.NoError(t, err)
	require.NotContains(t, r.calls, "resolve",
		"nothing is resolved for a representation that was never selected")
}

// TestResolutionIsNotCachedAcrossDeliveries guards the optimization that would quietly break
// invariant 6's word "current".
//
// Authorization can change between two deliveries of the same handle — a scope expires, a policy
// tightens. A pipeline that remembered the first answer would keep admitting on a decision that no
// longer holds, and the failure would be invisible because the record would still say resolvable.
func TestResolutionIsNotCachedAcrossDeliveries(t *testing.T) {
	r := newRecorder()
	p := admission.NewPipeline(admitting, r.ports())

	first, err := p.Admit(context.Background(), delivery)
	require.NoError(t, err)
	require.Equal(t, admission.OutcomeTransform, first.Outcome)

	r.handleState = admission.HandleDenied
	second, err := p.Admit(context.Background(), delivery)

	require.NoError(t, err)
	require.Equal(t, admission.OutcomePassThrough, second.Outcome,
		"authorization is asked per delivery; the earlier answer is not evidence about this one")
	require.Len(t, r.resolved, 2, "both deliveries put the question to the resolver")
}

// TestHandleStateRendersDistinctNames covers the String method, for the reason the other enum
// tests give: a forgotten arm turns a real resolution answer into "unknown", and here that word is
// already taken by a real state — so a missing arm would masquerade as HandleUnknown specifically.
func TestHandleStateRendersDistinctNames(t *testing.T) {
	seen := map[string]bool{}
	for _, s := range []admission.HandleState{
		admission.HandleUnknown, admission.HandleResolvable, admission.HandleDenied,
		admission.HandleUnavailable, admission.HandleUncertain,
	} {
		name := s.String()
		require.False(t, seen[name], "%q is rendered by two states", name)
		seen[name] = true
	}
	require.Equal(t, "unrecognized", admission.HandleState(99).String(),
		`an unmapped state must not render as "unknown", which is a real state here`)
}

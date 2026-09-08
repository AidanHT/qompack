package admission_test

import (
	"context"
	"testing"

	"github.com/qompack/qompack/internal/admission"
	"github.com/stretchr/testify/require"
)

// SP-21 commit 5's contracts: T21-RECURSE-01, and the part of the T21-HOST-01 competing-hook matrix
// that can be decided without an installed host.
//
// What can be tested here is the DECISION: given the markers an adapter observed, does admission
// run. What cannot be tested here is the observation — whether the installed host actually delivers
// competing hooks in a stable order, and what schema its own transformations take. B01 keeps that
// unverified, so the allowlist stays empty and the record never claims an order it did not observe.
// TestTheRecordNeverClaimsHookOrder is that limit written down as a test rather than a comment.

const foreign = "some.other.hook"

// TestAFreshOwnedResultIsEligible is the control for every bypass below: with no markers observed,
// admission runs. Without it, a bug that bypassed everything would make the whole file pass.
func TestAFreshOwnedResultIsEligible(t *testing.T) {
	r := newRecorder()
	p := admission.NewPipeline(admitting, r.ports())

	rec, err := p.Admit(context.Background(), delivery)

	require.NoError(t, err)
	require.Equal(t, admission.OutcomeTransform, rec.Outcome)
	require.Contains(t, r.calls, "capture")
}

// TestOurOwnEnvelopeBypasses is invariant 4's first clause, and invariant 3's "transforms never
// chain" made concrete.
//
// A result carrying our marker is one we already replaced. Admitting it again would build a capsule
// of a capsule, and each pass would move the reader one step further from what the tool actually
// said.
func TestOurOwnEnvelopeBypasses(t *testing.T) {
	r := newRecorder()
	p := admission.NewPipeline(admitting, r.ports())

	d := delivery
	d.Envelope = admission.Envelope{Markers: []string{admission.MarkerProducer}}

	rec, err := p.Admit(context.Background(), d)

	require.NoError(t, err)
	require.Equal(t, admission.OutcomePassThrough, rec.Outcome)
	require.Equal(t, admission.ReasonAlreadyProcessed, rec.Reason)
	require.Equal(t, []string{"privacy"}, r.calls,
		"privacy is still asked — it outranks every optimization decision — but nothing else runs")
}

// TestAnotherHooksTransformationBypasses is M4-04's "another hook's transformation" case.
//
// It is a separate reason from our own marker on purpose. "We already did this" and "somebody else
// already did this" are different facts: the first is admission working correctly, the second is a
// coexistence observation an operator may want to act on. Both refuse, because we do not transform
// what another hook has already rewritten — that is the same chaining invariant 3 forbids, with a
// different author.
func TestAnotherHooksTransformationBypasses(t *testing.T) {
	r := newRecorder()
	p := admission.NewPipeline(admitting, r.ports())

	d := delivery
	d.Envelope = admission.Envelope{Markers: []string{foreign}}

	rec, err := p.Admit(context.Background(), d)

	require.NoError(t, err)
	require.Equal(t, admission.OutcomePassThrough, rec.Outcome)
	require.Equal(t, admission.ReasonForeignTransform, rec.Reason)
	require.Equal(t, []string{"privacy"}, r.calls)
}

// TestBypassHappensBeforeAnythingIsCaptured is why the bypass sits where it does.
//
// A processed envelope's original was already captured on the pass that produced it. Capturing the
// envelope too would archive a second object that is a transformation of the first, and a store
// that accumulates one of those per redelivery is the recursion invariant 3 forbids showing up as
// disk usage instead of as nested capsules.
func TestBypassHappensBeforeAnythingIsCaptured(t *testing.T) {
	for name, markers := range map[string][]string{
		"ours":    {admission.MarkerProducer},
		"foreign": {foreign},
		"both":    {foreign, admission.MarkerProducer},
	} {
		t.Run(name, func(t *testing.T) {
			r := newRecorder()
			p := admission.NewPipeline(admitting, r.ports())

			d := delivery
			d.Envelope = admission.Envelope{Markers: markers}

			rec, err := p.Admit(context.Background(), d)

			require.NoError(t, err)
			require.NotContains(t, r.calls, "capture")
			require.Empty(t, rec.Handle)
			require.Equal(t, admission.FormNone, rec.Form)
		})
	}
}

// TestAdmissionIsIdempotentAcrossRedelivery is invariant 4's second clause, proven as a round trip
// rather than asserted.
//
// The first pass admits and tells the caller which marker to stamp. The second pass receives what
// that caller would have produced, and bypasses. This is the whole idempotence mechanism: admission
// holds no per-result memory, so a repeated delivery is recognized by what it carries, which is the
// only thing that survives a daemon restart.
func TestAdmissionIsIdempotentAcrossRedelivery(t *testing.T) {
	r := newRecorder()
	p := admission.NewPipeline(admitting, r.ports())

	first, err := p.Admit(context.Background(), delivery)
	require.NoError(t, err)
	require.Equal(t, admission.OutcomeTransform, first.Outcome)
	require.Equal(t, admission.MarkerProducer, first.Mark,
		"an admitted record must tell the caller what to stamp, or nothing marks the envelope")

	// Exactly what a caller that obeyed first.Mark would deliver next time.
	redelivered := delivery
	redelivered.Envelope = admission.Envelope{Markers: []string{first.Mark}}

	second, err := p.Admit(context.Background(), redelivered)

	require.NoError(t, err)
	require.Equal(t, admission.OutcomePassThrough, second.Outcome)
	require.Equal(t, admission.ReasonAlreadyProcessed, second.Reason)
	require.Equal(t, first.Target, second.Target,
		"both passes decided about the same delivery")
}

// TestARefusedRecordCarriesNoMark keeps the stamp instruction tied to an actual emission.
//
// A mark on a passed-through record would tell a caller to stamp an envelope it never produced,
// and the next delivery of that untransformed result would then bypass admission forever.
func TestARefusedRecordCarriesNoMark(t *testing.T) {
	r := newRecorder()
	r.handleState = admission.HandleDenied
	p := admission.NewPipeline(admitting, r.ports())

	rec, err := p.Admit(context.Background(), delivery)

	require.NoError(t, err)
	require.Equal(t, admission.OutcomePassThrough, rec.Outcome)
	require.Empty(t, rec.Mark, "nothing was emitted, so there is nothing to stamp")
}

// TestDuplicateMarkersBypassAndStayVisible is M4-04's "duplicate markers" case.
//
// Two copies of our own marker means something stamped twice, which is an anomaly worth seeing.
// The decision is unchanged — it is still processed — but collapsing the duplicates in the record
// would hide the only evidence that it happened.
func TestDuplicateMarkersBypassAndStayVisible(t *testing.T) {
	r := newRecorder()
	p := admission.NewPipeline(admitting, r.ports())

	d := delivery
	d.Envelope = admission.Envelope{
		Markers: []string{admission.MarkerProducer, admission.MarkerProducer},
	}

	rec, err := p.Admit(context.Background(), d)

	require.NoError(t, err)
	require.Equal(t, admission.ReasonAlreadyProcessed, rec.Reason)
	require.Equal(t, []string{admission.MarkerProducer, admission.MarkerProducer}, rec.Observed,
		"a duplicate stamp is an anomaly; deduplicating it in the record destroys the evidence")
}

// TestTheRecordNeverClaimsHookOrder is M4-04's limit stated as a test: "the admission record
// identifies the observed chain without claiming unobserved order."
//
// Two things follow, and both are asserted. The record reports markers exactly as the adapter read
// them — no sorting, which would invent a canonical order nobody observed. And the DECISION does
// not depend on that order, so a host that delivers competing hooks in a different sequence than
// the last run cannot flip admission on or off.
func TestTheRecordNeverClaimsHookOrder(t *testing.T) {
	forward := []string{foreign, admission.MarkerProducer}
	backward := []string{admission.MarkerProducer, foreign}

	decide := func(markers []string) admission.Record {
		r := newRecorder()
		p := admission.NewPipeline(admitting, r.ports())
		d := delivery
		d.Envelope = admission.Envelope{Markers: markers}
		rec, err := p.Admit(context.Background(), d)
		require.NoError(t, err)
		return rec
	}

	a, b := decide(forward), decide(backward)

	require.Equal(t, a.Outcome, b.Outcome, "marker order must not change the decision")
	require.Equal(t, a.Reason, b.Reason)
	require.Equal(t, forward, a.Observed, "the record reports what was read, unsorted")
	require.Equal(t, backward, b.Observed)
}

// TestOurMarkerOutranksAForeignOneInTheReason fixes the reported reason when both are present, so
// the record stays deterministic under the ambiguity the test above deliberately allows.
//
// Ours is the more specific fact: whatever else touched this result, we are looking at output we
// produced, and that is what makes re-admitting it a chained transform of our own work.
func TestOurMarkerOutranksAForeignOneInTheReason(t *testing.T) {
	r := newRecorder()
	p := admission.NewPipeline(admitting, r.ports())

	d := delivery
	d.Envelope = admission.Envelope{Markers: []string{foreign, admission.MarkerProducer}}

	rec, err := p.Admit(context.Background(), d)

	require.NoError(t, err)
	require.Equal(t, admission.ReasonAlreadyProcessed, rec.Reason)
}

// TestAnEmptyMarkerIsNotAMarker is the adapter robustness rule.
//
// An adapter that reads a field absent from the payload gets the empty string. Treating that as a
// marker would bypass admission for every unmarked result — the failure would look like admission
// simply never running, with no error anywhere to explain it.
func TestAnEmptyMarkerIsNotAMarker(t *testing.T) {
	require.False(t, admission.Envelope{Markers: []string{""}}.Processed(),
		"an absent field reads as the empty string; that is not a marker")
	require.False(t, admission.Envelope{Markers: []string{"", "  "}}.Processed(),
		"whitespace is not a marker either")
	require.False(t, admission.Envelope{}.Processed(), "the zero Envelope is a fresh result")

	r := newRecorder()
	p := admission.NewPipeline(admitting, r.ports())
	d := delivery
	d.Envelope = admission.Envelope{Markers: []string{""}}

	rec, err := p.Admit(context.Background(), d)

	require.NoError(t, err)
	require.Equal(t, admission.OutcomeTransform, rec.Outcome,
		"a blank marker must not silently disable admission for every result")
}

// TestADisabledGateOutranksAProcessedEnvelope keeps the record's precedence the same as commit 2
// set it: when more than one thing would refuse, the switch is the reported reason.
//
// Both answers pass through, so nothing about the delivery changes. What changes is what an
// operator reads: "disabled" is the fact they can act on, and "already-processed" would send them
// looking for a recursion in a pipeline that was never running.
func TestADisabledGateOutranksAProcessedEnvelope(t *testing.T) {
	r := newRecorder()
	p := admission.NewPipeline(admission.Gate{}, r.ports())

	d := delivery
	d.Envelope = admission.Envelope{Markers: []string{admission.MarkerProducer}}

	rec, err := p.Admit(context.Background(), d)

	require.NoError(t, err)
	require.Equal(t, admission.ReasonDisabled, rec.Reason)
}

// TestPrivacyOutranksTheBypass keeps the one rule that survives everything.
//
// Qompack.md §7.1: privacy-denied data follows privacy policy even when optimization otherwise
// fails toward pass-through. A processed envelope is still a delivery, and bypassing admission is
// not a reason to deliver content policy refused.
func TestPrivacyOutranksTheBypass(t *testing.T) {
	r := newRecorder()
	r.denyPrivacy = true
	p := admission.NewPipeline(admitting, r.ports())

	d := delivery
	d.Envelope = admission.Envelope{Markers: []string{admission.MarkerProducer}}

	rec, err := p.Admit(context.Background(), d)

	require.NoError(t, err)
	require.Equal(t, admission.OutcomeDeny, rec.Outcome,
		"an already-processed envelope is still subject to privacy policy")
	require.Equal(t, admission.ReasonPrivacyDenied, rec.Reason)
}

// TestTheHostAllowlistIsEmptyAndSaysWhy is T21-HOST-01's explicit unverified disposition, which the
// plan permits in place of an installed-host canary transcript.
//
// It is a test rather than a comment because the disposition is a shipped property: the moment
// someone adds a host target here without the canary evidence B01 blocks, this fails.
func TestTheHostAllowlistIsEmptyAndSaysWhy(t *testing.T) {
	var shipped admission.Gate

	require.True(t, shipped.Allow.Empty(),
		"B01 keeps installed host versions and permissions unverified, so no host schema has the "+
			"canary transcript and competing-hook observation SP-21 requires before admitting it")

	// And with the switch on but nothing owned, every host target still passes through.
	on := admission.Gate{Enabled: true}
	for _, target := range []admission.Target{
		{Schema: "some.host.tool_result", Version: "1"},
		{Schema: "qompack.retrieval", Version: "1"},
	} {
		ok, why := on.Admits(target)
		require.False(t, ok, "%v must not be admitted without target evidence", target)
		require.Equal(t, admission.ReasonUnknownTarget, why)
	}
}

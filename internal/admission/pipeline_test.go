package admission_test

import (
	"context"
	"errors"
	"testing"

	"github.com/qompack/qompack/internal/admission"
	"github.com/stretchr/testify/require"
)

// T21-PIPE-01: capture and publication verify before one transform or handle emission.
//
// The ports are fakes here rather than a real store, and that is the point of the seam rather than
// a shortcut. internal/admission is foundation-only under §3.2, so it reaches SP-20 through
// interfaces it declares and a composition root satisfies. What these tests grade is the ORDER and
// the REFUSALS — properties of the sequence, which a fake exercises exactly as well as a real store
// and far more precisely, because a fake can fail on demand at a chosen stage. The adapter that
// binds these ports to store.PutBytes is a later commit and belongs to a composition root.
//
// admission.Capture mirrors the recovery-relevant half of store.PutResult — handle, fidelity,
// truncation, redaction count — without importing it.

// recorder is a port set that logs the order it was called in, so ordering can be asserted
// directly instead of inferred from side effects.
type recorder struct {
	calls []string

	denyPrivacy bool
	captureErr  error
	publishErr  error
	parseErr    error
	fidelity    admission.Fidelity
	handle      string
	meaning     admission.Meaning

	// gate lets a table-driven case vary the gate alongside the ports it is already varying.
	gate admission.Gate
}

func newRecorder() *recorder {
	return &recorder{
		fidelity: admission.FidelityExact,
		handle:   "sha256:" + hex64,
		meaning:  meaning(),
		gate:     admitting,
	}
}

const hex64 = "1111111111111111111111111111111111111111111111111111111111111111"

func (r *recorder) Permits(_ context.Context, _ []byte) (bool, error) {
	r.calls = append(r.calls, "privacy")
	return !r.denyPrivacy, nil
}

func (r *recorder) Capture(_ context.Context, _ []byte) (admission.Capture, error) {
	r.calls = append(r.calls, "capture")
	if r.captureErr != nil {
		return admission.Capture{}, r.captureErr
	}
	return admission.Capture{Handle: r.handle, Fidelity: r.fidelity}, nil
}

func (r *recorder) VerifyPublished(_ context.Context, _ admission.Capture) error {
	r.calls = append(r.calls, "publish")
	return r.publishErr
}

func (r *recorder) Parse(_ context.Context, _ admission.Delivery) (admission.Meaning, error) {
	r.calls = append(r.calls, "parse")
	if r.parseErr != nil {
		return admission.Meaning{}, r.parseErr
	}
	return r.meaning, nil
}

func (r *recorder) ports() admission.Ports {
	return admission.Ports{Privacy: r, Capture: r, Publish: r, Parse: r}
}

// delivery is the result under admission. The payload stands for whatever the host delivered.
var delivery = admission.Delivery{
	Target:  admission.Target{Schema: "qompack.retrieval", Version: "1"},
	Payload: []byte("a delivered result"),
}

// admitting is the gate that permits an owned result.
var admitting = admission.Gate{Enabled: true, Owned: true}

// TestAdmitCapturesBeforeItTransforms is T21-PIPE-01's headline: the sequence is fixed, and
// capture and publication both complete before anything is admitted.
func TestAdmitCapturesBeforeItTransforms(t *testing.T) {
	r := newRecorder()
	p := admission.NewPipeline(admitting, r.ports())

	rec, err := p.Admit(context.Background(), delivery)

	require.NoError(t, err)
	require.Equal(t, []string{"privacy", "capture", "publish", "parse"}, r.calls,
		"the sequence is privacy, then capture, then publication verification, then the parse "+
			"commit 3 appended; selection is pure and needs no port")
	require.Equal(t, admission.OutcomeTransform, rec.Outcome)
	require.Equal(t, r.handle, rec.Handle,
		"an admitted record must name the handle its capture produced")
}

// TestPrivacyIsConsultedBeforeAnythingIsPersisted is SP-20 invariant 1 seen from M4: privacy
// denial and redaction occur BEFORE persistence.
//
// The assertion that matters is that "capture" never appears. Consulting privacy after writing the
// bytes would archive the very content policy refused, and no later denial can unwrite it.
func TestPrivacyIsConsultedBeforeAnythingIsPersisted(t *testing.T) {
	r := newRecorder()
	r.denyPrivacy = true
	p := admission.NewPipeline(admitting, r.ports())

	rec, err := p.Admit(context.Background(), delivery)

	require.NoError(t, err)
	require.Equal(t, []string{"privacy"}, r.calls,
		"a denied delivery must never reach capture; persistence after denial cannot be undone")
	require.Equal(t, admission.OutcomeDeny, rec.Outcome)
	require.Equal(t, admission.ReasonPrivacyDenied, rec.Reason)
}

// TestCaptureFailureForbidsReplacement is SP-21 invariant 2 and M4-02's "capture failure forbids
// pointer replacement".
func TestCaptureFailureForbidsReplacement(t *testing.T) {
	r := newRecorder()
	r.captureErr = errors.New("disk full")
	p := admission.NewPipeline(admitting, r.ports())

	rec, err := p.Admit(context.Background(), delivery)

	require.NoError(t, err, "a failed stage is a pass-through, not an error to the caller")
	require.Equal(t, []string{"privacy", "capture"}, r.calls,
		"publication must not be consulted for a capture that failed")
	require.Equal(t, admission.OutcomePassThrough, rec.Outcome)
	require.Equal(t, admission.ReasonCaptureFailed, rec.Reason)
	require.Empty(t, rec.Handle, "a failed capture must not emit a handle")
	require.ErrorContains(t, rec.Err, "disk full", "the record retains the stage error for audit")
}

// TestUnverifiedPublicationForbidsReplacement is the second half of invariant 2. A capture that
// succeeded but whose publication cannot be verified is not yet durable, and durability is the
// whole reason M4 follows M1.
func TestUnverifiedPublicationForbidsReplacement(t *testing.T) {
	r := newRecorder()
	r.publishErr = errors.New("frontier not committed")
	p := admission.NewPipeline(admitting, r.ports())

	rec, err := p.Admit(context.Background(), delivery)

	require.NoError(t, err)
	require.Equal(t, []string{"privacy", "capture", "publish"}, r.calls)
	require.Equal(t, admission.OutcomePassThrough, rec.Outcome)
	require.Equal(t, admission.ReasonPublicationUnverified, rec.Reason)
	require.Empty(t, rec.Handle, "an unverified publication must not emit a handle")
}

// TestCanonicalOnlyCaptureForbidsReplacement is the rule store.FidelityCanonical states in its own
// doc comment: canonical bytes "are NOT the original".
//
// This is the subtle one. The capture SUCCEEDED and the publication verified, so both refusals
// above pass. But what was retained is not what was delivered, and replacing a delivered result
// with a pointer to something else is precisely the data loss M4 exists to prevent. Fidelity is the
// only field that distinguishes the two, and SP-20 guarantees it is never a guess.
func TestCanonicalOnlyCaptureForbidsReplacement(t *testing.T) {
	r := newRecorder()
	r.fidelity = admission.FidelityCanonical
	p := admission.NewPipeline(admitting, r.ports())

	rec, err := p.Admit(context.Background(), delivery)

	require.NoError(t, err)
	require.Equal(t, admission.OutcomePassThrough, rec.Outcome,
		"canonical-only bytes are not the original, so nothing may point away from the original")
	require.Equal(t, admission.ReasonCaptureFailed, rec.Reason)
	require.Empty(t, rec.Handle)
}

// TestRecoverableFidelitiesAdmit is the positive side of the same rule, asserted for both values
// SP-20 defines as recoverable so that neither is refused by accident.
func TestRecoverableFidelitiesAdmit(t *testing.T) {
	for _, f := range []admission.Fidelity{admission.FidelityExact, admission.FidelityFull} {
		r := newRecorder()
		r.fidelity = f
		p := admission.NewPipeline(admitting, r.ports())

		rec, err := p.Admit(context.Background(), delivery)

		require.NoError(t, err)
		require.Equal(t, admission.OutcomeTransform, rec.Outcome,
			"fidelity %q recovers the original and must admit", f)
		require.True(t, f.Recoverable())
	}
	require.False(t, admission.FidelityCanonical.Recoverable())
	require.False(t, admission.Fidelity("").Recoverable(),
		"an unset fidelity is not a recoverable one; absent evidence is not evidence")
}

// TestDisabledGateCapturesNothing keeps the shipped state cheap as well as safe. With admission
// off there is nothing to replace, so the pipeline must not persist a second copy of every
// delivered result on the way to refusing.
//
// Privacy is still consulted first — it is not part of the optimization — but nothing is written.
func TestDisabledGateCapturesNothing(t *testing.T) {
	r := newRecorder()
	p := admission.NewPipeline(admission.Gate{}, r.ports())

	rec, err := p.Admit(context.Background(), delivery)

	require.NoError(t, err)
	require.Equal(t, []string{"privacy"}, r.calls,
		"a disabled pipeline must not capture; there is nothing for it to replace")
	require.Equal(t, admission.OutcomePassThrough, rec.Outcome)
	require.Equal(t, admission.ReasonDisabled, rec.Reason)
}

// TestUnknownTargetCapturesNothing is the same economy for a host schema nobody has evidence for.
func TestUnknownTargetCapturesNothing(t *testing.T) {
	r := newRecorder()
	p := admission.NewPipeline(admission.Gate{Enabled: true}, r.ports())

	rec, err := p.Admit(context.Background(), delivery)

	require.NoError(t, err)
	require.Equal(t, []string{"privacy"}, r.calls)
	require.Equal(t, admission.ReasonUnknownTarget, rec.Reason)
}

// TestMissingPortsRefuseRatherThanPanic covers the composition-root mistake this package cannot
// prevent: a Pipeline built with a port left nil.
//
// A nil port must refuse, not panic and not silently admit. A panic here reaches a hook, and §2.3
// permits a hook no exit code but zero.
func TestMissingPortsRefuseRatherThanPanic(t *testing.T) {
	p := admission.NewPipeline(admitting, admission.Ports{})

	rec, err := p.Admit(context.Background(), delivery)

	require.NoError(t, err)
	require.Equal(t, admission.OutcomePassThrough, rec.Outcome)
	require.Equal(t, admission.ReasonPolicyUnavailable, rec.Reason,
		"an unwired pipeline could not ask policy, which is not the same as policy refusing")
}

// TestPrivacyErrorPassesThroughWithoutCapturing fixes the answer when the privacy port itself
// fails, and it is the assertion this pipeline got wrong first.
//
// The tempting rule is "an undecidable check is not permission, so deny". That conflates two
// different facts. Denial WITHHOLDS the delivered result from the user; pass-through delivers the
// original untouched, which is what they would have seen with no plugin installed at all. A
// configuration mistake must not be able to withhold a user's tool output.
//
// What both answers share, and all that they share, is that nothing is persisted: an unanswerable
// policy check clears nothing for capture. That is the property asserted on r.calls below.
func TestPrivacyErrorPassesThroughWithoutCapturing(t *testing.T) {
	r := &recorder{fidelity: admission.FidelityExact, handle: "sha256:" + hex64}
	p := admission.NewPipeline(admitting, admission.Ports{
		Privacy: privacyErr{r}, Capture: r, Publish: r,
	})

	rec, err := p.Admit(context.Background(), delivery)

	require.NoError(t, err)
	require.Equal(t, admission.OutcomePassThrough, rec.Outcome,
		"a policy that could not be evaluated must not withhold the user's own result")
	require.Equal(t, admission.ReasonPolicyUnavailable, rec.Reason)
	require.NotEqual(t, admission.OutcomeDeny, rec.Outcome,
		"only an explicit refusal denies; ReasonPrivacyDenied is reserved for a real decision")
	require.Empty(t, r.calls, "nothing may be captured when policy could not be evaluated")
}

// privacyErr is a privacy port whose check fails.
type privacyErr struct{ r *recorder }

func (privacyErr) Permits(_ context.Context, _ []byte) (bool, error) {
	return false, errors.New("policy unavailable")
}

// TestPrivacyWiredButCaptureMissingRefuses covers the half-wired composition root: policy present,
// storage forgotten.
//
// TestMissingPortsRefuseRatherThanPanic cannot reach this branch — with every port nil the privacy
// check short-circuits first — so without this case the nil-capture guard is unreachable code that
// looks like protection. Wiring policy and omitting storage is the more likely mistake of the two,
// because policy is the port a root is most likely to remember.
func TestPrivacyWiredButCaptureMissingRefuses(t *testing.T) {
	r := newRecorder()

	p := admission.NewPipeline(admitting, admission.Ports{Privacy: r})

	rec, err := p.Admit(context.Background(), delivery)

	require.NoError(t, err)
	require.Equal(t, []string{"privacy"}, r.calls, "policy is asked; storage is not there to ask")
	require.Equal(t, admission.OutcomePassThrough, rec.Outcome)
	require.Equal(t, admission.ReasonCaptureFailed, rec.Reason,
		"an absent capture port has captured nothing, which forbids replacement")
	require.ErrorIs(t, rec.Err, admission.ErrPortsUnwired)
	require.Empty(t, rec.Handle)
}

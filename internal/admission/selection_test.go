package admission_test

import (
	"context"
	"errors"
	"testing"

	"github.com/qompack/qompack/internal/admission"
	"github.com/qompack/qompack/internal/core"
	"github.com/stretchr/testify/require"
)

// This file wires commit 3's representation rules into the pipeline. capsule_test.go grades Select
// as a pure decision; these grade the two things only the sequence can answer: WHEN parsing and
// selection happen relative to capture, and what a record carries when they refuse.

// TestParseRunsAfterCaptureAndPublication fixes the position of the new stages.
//
// Parsing after capture is not an efficiency choice. Both representations point at the captured
// original, so the parse exists to describe something already durable; parsing first would let a
// pipeline build a representation of bytes that were never retained, and then discover it has
// nothing to point at.
func TestParseRunsAfterCaptureAndPublication(t *testing.T) {
	r := newRecorder()
	p := admission.NewPipeline(admitting, r.ports())

	rec, err := p.Admit(context.Background(), delivery)

	require.NoError(t, err)
	require.Equal(t, []string{"privacy", "capture", "publish", "parse"}, r.calls,
		"the meaning is parsed only once the original it describes is durably captured")
	require.Equal(t, admission.OutcomeTransform, rec.Outcome)
}

// TestCaptureFailureNeverReachesTheParser is the same rule seen from the failure side, and it is
// the version that can regress silently: reordering the stages would still pass the test above.
func TestCaptureFailureNeverReachesTheParser(t *testing.T) {
	r := newRecorder()
	r.captureErr = errors.New("disk full")
	p := admission.NewPipeline(admitting, r.ports())

	_, err := p.Admit(context.Background(), delivery)

	require.NoError(t, err)
	require.NotContains(t, r.calls, "parse",
		"there is nothing to describe when the original was never captured")
}

// TestParseFailurePassesThroughTheOriginal is T21-PASS-01 applied to the parse stage.
func TestParseFailurePassesThroughTheOriginal(t *testing.T) {
	r := newRecorder()
	r.parseErr = errors.New("unexpected end of JSON input")
	p := admission.NewPipeline(admitting, r.ports())

	rec, err := p.Admit(context.Background(), delivery)

	require.NoError(t, err, "a failed stage is a pass-through, not an error to the caller")
	require.Equal(t, admission.OutcomePassThrough, rec.Outcome)
	require.Equal(t, admission.ReasonParseFailed, rec.Reason)
	require.ErrorContains(t, rec.Err, "unexpected end of JSON input")
	require.Equal(t, admission.FormNone, rec.Form, "a payload nobody could parse has no form")
}

// TestNilParserPortRefusesRatherThanPanicking covers the composition root that wired storage and
// forgot the parser — the same class as TestPrivacyWiredButCaptureMissingRefuses, and reachable
// only once capture and publication are wired, which is why it needs its own case.
func TestNilParserPortRefusesRatherThanPanicking(t *testing.T) {
	r := newRecorder()
	p := admission.NewPipeline(admitting, admission.Ports{Privacy: r, Capture: r, Publish: r})

	rec, err := p.Admit(context.Background(), delivery)

	require.NoError(t, err)
	require.Equal(t, admission.OutcomePassThrough, rec.Outcome)
	require.Equal(t, admission.ReasonParseFailed, rec.Reason)
	require.ErrorIs(t, rec.Err, admission.ErrPortsUnwired)
}

// TestMediaPayloadSelectsNothingAndPassesThrough is M4-03's binary/multimodal clause reaching a
// caller: the selection refuses, and the refusal is a pass-through of the exact original rather
// than a denial.
func TestMediaPayloadSelectsNothingAndPassesThrough(t *testing.T) {
	r := newRecorder()
	r.meaning.Media = true
	p := admission.NewPipeline(admitting, r.ports())

	rec, err := p.Admit(context.Background(), delivery)

	require.NoError(t, err)
	require.Equal(t, admission.OutcomePassThrough, rec.Outcome,
		"an image is delivered as it arrived, not withheld")
	require.Equal(t, admission.ReasonNoRepresentation, rec.Reason)
	require.Equal(t, admission.StageSelection, rec.Stage)
	require.Equal(t, admission.FormNone, rec.Form)
}

// TestAnAdmittedRecordPreservesEveryMeaningField is T21-FIDELITY-01 at the pipeline boundary.
//
// capsule_test.go proves Preserves detects a lost field. This proves the pipeline does not lose
// one: the meaning the parser produced is the meaning the record carries, field for field.
func TestAnAdmittedRecordPreservesEveryMeaningField(t *testing.T) {
	r := newRecorder()
	r.meaning = meaning()
	p := admission.NewPipeline(admitting, r.ports())

	rec, err := p.Admit(context.Background(), delivery)

	require.NoError(t, err)
	require.Equal(t, admission.OutcomeTransform, rec.Outcome)
	require.Empty(t, admission.Preserves(meaning(), rec.Meaning),
		"every structured and displayed field must reach the record intact")
}

// TestAnAdmittedRecordCarriesItsFormAndCoverage checks the two facts the parser is not allowed to
// supply, and that Meaning therefore has no field for.
//
// Fidelity comes from the capture. Coverage is archive-only because that is what admission does:
// the representation stays in context and the original bytes live in the archive. Neither may be
// left unset — an empty coverage string reads as a value nobody chose.
func TestAnAdmittedRecordCarriesItsFormAndCoverage(t *testing.T) {
	r := newRecorder()
	p := admission.NewPipeline(admitting, r.ports())

	rec, err := p.Admit(context.Background(), delivery)

	require.NoError(t, err)
	require.Equal(t, admission.FormCapsule, rec.Form,
		"with no baseline offered, the first representation is a self-contained capsule")
	require.Equal(t, admission.ResetNoBaseline, rec.Reset)
	require.Empty(t, rec.Base)
	require.Equal(t, admission.FidelityExact, rec.Fidelity, "fidelity is the capture's answer")
	require.Equal(t, core.CoverageArchiveOnly, rec.Coverage,
		"an admitted representation leaves the original bytes in the archive")
}

// TestADeliveredBaselineReachesTheSelection is the pipeline half of T21-BASELINE-01: a verified
// compatible baseline offered on the delivery produces a delta that names it.
func TestADeliveredBaselineReachesTheSelection(t *testing.T) {
	r := newRecorder()
	r.meaning = meaning()
	p := admission.NewPipeline(admitting, r.ports())

	d := delivery
	d.Baseline = admission.VerifyBaseline("toolresult:prior",
		r.meaning.Signature, r.meaning.Schema, admission.FidelityFull)

	rec, err := p.Admit(context.Background(), d)

	require.NoError(t, err)
	require.Equal(t, admission.FormDelta, rec.Form)
	require.Equal(t, "toolresult:prior", rec.Base)
	require.Equal(t, admission.ResetNone, rec.Reset)
}

// TestAnUnverifiedDeliveredBaselineStillProducesACapsule is the same path with the shortcut a
// caller is most likely to take, and the reason Baseline's verification is unexported: a delivery
// that carries a prior identity it never read back gets a capsule, not a delta.
func TestAnUnverifiedDeliveredBaselineStillProducesACapsule(t *testing.T) {
	r := newRecorder()
	p := admission.NewPipeline(admitting, r.ports())

	d := delivery
	d.Baseline = admission.Baseline{ID: "toolresult:prior"}

	rec, err := p.Admit(context.Background(), d)

	require.NoError(t, err)
	require.Equal(t, admission.FormCapsule, rec.Form)
	require.Equal(t, admission.ResetUnverified, rec.Reset)
	require.Empty(t, rec.Base, "a capsule names no baseline")
}

// TestARefusedRecordCarriesNoRepresentation sweeps every refusal the pipeline can reach and
// requires all of them to leave the representation fields empty.
//
// A refusal that filled in a form, a meaning or a handle would give a caller something that looks
// usable. The point of the sweep rather than one representative case is that these refusals come
// from different stages and different owners, and a later edit that populated the record on one of
// them is exactly the defect this catches.
func TestARefusedRecordCarriesNoRepresentation(t *testing.T) {
	for name, setup := range map[string]func(*recorder){
		"privacy denied":  func(r *recorder) { r.denyPrivacy = true },
		"capture failed":  func(r *recorder) { r.captureErr = errors.New("disk full") },
		"publish failed":  func(r *recorder) { r.publishErr = errors.New("frontier uncommitted") },
		"canonical only":  func(r *recorder) { r.fidelity = admission.FidelityCanonical },
		"parse failed":    func(r *recorder) { r.parseErr = errors.New("malformed") },
		"media payload":   func(r *recorder) { r.meaning.Media = true },
		"gate is refused": func(r *recorder) { r.gate = admission.Gate{} },
	} {
		t.Run(name, func(t *testing.T) {
			r := newRecorder()
			r.meaning = meaning()
			r.gate = admitting
			setup(r)
			p := admission.NewPipeline(r.gate, r.ports())

			rec, err := p.Admit(context.Background(), delivery)

			require.NoError(t, err)
			require.NotEqual(t, admission.OutcomeTransform, rec.Outcome)
			require.Equal(t, admission.FormNone, rec.Form, "a refusal selects no form")
			require.Empty(t, rec.Base)
			require.Equal(t, admission.ResetNone, rec.Reset)
			require.Zero(t, rec.Meaning, "a refusal carries no meaning to act on")
			require.Empty(t, rec.Handle, "a refusal emits no handle")
			require.Equal(t, core.CoverageUnknown, rec.Coverage,
				"admission did not observe where a passed-through original ended up")
		})
	}
}

// TestAParsedMeaningMustAgreeWithTheDeclaredTarget closes the gap between invariant 5's exact
// dispatch and what the parser hands back.
//
// The gate matched the target the delivery DECLARED. If the parser then returns a meaning under a
// different schema, it saw something other than what dispatch selected it for, and the record would
// carry two disagreeing answers about what the representation describes. Neither is safe to prefer,
// so the delivery passes through.
func TestAParsedMeaningMustAgreeWithTheDeclaredTarget(t *testing.T) {
	for name, schema := range map[string]admission.Target{
		"different version": {Schema: "qompack.retrieval", Version: "2"},
		"different schema":  {Schema: "qompack.other", Version: "1"},
		"unset":             {},
	} {
		t.Run(name, func(t *testing.T) {
			r := newRecorder()
			r.meaning.Schema = schema
			p := admission.NewPipeline(admitting, r.ports())

			rec, err := p.Admit(context.Background(), delivery)

			require.NoError(t, err)
			require.Equal(t, admission.OutcomePassThrough, rec.Outcome)
			require.Equal(t, admission.ReasonParseFailed, rec.Reason,
				"a meaning under schema %v does not describe a %v delivery", schema, delivery.Target)
			require.Equal(t, admission.FormNone, rec.Form)
		})
	}
}

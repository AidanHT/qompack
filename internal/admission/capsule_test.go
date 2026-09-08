package admission_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/qompack/qompack/internal/admission"
	"github.com/stretchr/testify/require"
)

// These are SP-21 commit 3's contracts: T21-FIDELITY-01 and T21-BASELINE-01, the representation
// rules from invariants 7, 8 and 9.
//
// Two things are deliberately NOT asserted here. Handle resolution is commit 4 and needs an SP-13
// seam. And no test populates the host allowlist, for the reason commit 1 gave: B01 keeps the
// installed host unverified, so a fixture that named a host schema would assert against evidence
// nobody has.

// meaning returns a fully populated Meaning. Every field is non-zero on purpose: a preservation
// test whose fixture leaves fields at their zero value cannot tell "preserved" from "dropped".
func meaning() admission.Meaning {
	return admission.Meaning{
		Status:      "error",
		Stderr:      "go: cannot find module",
		Diagnostics: []string{"exit status 1", "2 packages failed"},
		Interrupted: true,
		Media:       false,
		InputCount:  3,
		OutputCount: 91,
		Signature:   "build-failed:module-not-found",
		Source: admission.Source{
			ID:   "toolresult:abc123",
			Span: admission.Span{Start: 4096, End: 8192},
		},
		Schema:     admission.Target{Schema: "qompack.retrieval", Version: "1"},
		Structured: []byte(`{"ok":false}`),
		Displayed:  "build failed: module not found",
	}
}

// TestEveryMeaningFieldIsPreserved is T21-FIDELITY-01's field-by-field clause, and invariant 7.
//
// It walks Meaning reflectively and changes one field at a time, requiring Preserves to name that
// field and only that field. The failure it exists to catch is a hand-written field list in
// Preserves: someone adds Interrupted to Meaning, forgets the corresponding comparison, and a
// dropped interruption flag becomes invisible. A reflective walk cannot have that gap, and this
// test is what stops the walk from being "simplified" into an enumeration later.
func TestEveryMeaningFieldIsPreserved(t *testing.T) {
	base := meaning()
	typ := reflect.TypeOf(base)
	require.NotZero(t, typ.NumField(), "Meaning must carry the fields invariant 7 lists")

	for i := range typ.NumField() {
		name := typ.Field(i).Name

		changed := meaning()
		require.True(t, mutateField(reflect.ValueOf(&changed).Elem().Field(i)),
			"the test helper cannot alter field %s, so this field is unchecked", name)

		diff := admission.Preserves(base, changed)

		require.Equal(t, []string{name}, diff,
			"changing %s must be reported as exactly that field having been lost", name)
	}

	require.Empty(t, admission.Preserves(base, meaning()),
		"an unchanged meaning must report no lost fields")
}

// TestMeaningNeverClaimsFidelityOrCoverage is the anti-forgery rule behind invariant 7's placement
// of fidelity and coverage.
//
// Both survive on the admission record, not inside Meaning, because Meaning is what a PARSER
// produced and a parser knows neither. Fidelity is the capture's answer about whether the original
// bytes can come back; coverage is a lifecycle observation. A parser that could set either could
// declare a canonical-only capture "exact" and the record would carry the lie forward.
func TestMeaningNeverClaimsFidelityOrCoverage(t *testing.T) {
	typ := reflect.TypeOf(admission.Meaning{})

	for i := range typ.NumField() {
		name := strings.ToLower(typ.Field(i).Name)
		require.NotContains(t, name, "fidelity",
			"Meaning.%s: fidelity is the capture's answer, not the parser's", typ.Field(i).Name)
		require.NotContains(t, name, "coverage",
			"Meaning.%s: coverage is an observation, not a parsed field", typ.Field(i).Name)
	}
}

// TestFormNoneIsTheZeroValue keeps the enum ordering safe, the same way OutcomePassThrough is zero.
//
// A Selection nobody populated must emit nothing. If FormCapsule were zero, every unfilled
// Selection would authorize a replacement.
func TestFormNoneIsTheZeroValue(t *testing.T) {
	var sel admission.Selection

	require.Equal(t, admission.FormNone, sel.Form,
		"an unpopulated Selection must authorize no representation")
	require.False(t, sel.Form.Emits(), "FormNone emits no representation")
}

// TestBinaryAndMultimodalPayloadsSelectNoRepresentation is M4-03's pass-through clause.
//
// Qompack.md keeps binary and multimodal payloads out of admission entirely: there is no evidence
// that a capsule preserves anything an image or an archive means. The safe answer is to select
// nothing, which the pipeline turns into a pass-through of the exact original.
func TestBinaryAndMultimodalPayloadsSelectNoRepresentation(t *testing.T) {
	m := meaning()
	m.Media = true

	sel := admission.Select(m, admission.FidelityExact, admission.Baseline{})

	require.Equal(t, admission.FormNone, sel.Form,
		"a binary or multimodal payload has no representation this package can justify")
	require.Equal(t, admission.ReasonNoRepresentation, sel.Reason)
}

// TestAnUnrecoverableCaptureBacksNoRepresentation is invariant 2 restated at the representation
// boundary.
//
// Both a capsule and a delta point at the captured original. Pointing at bytes that are not the
// original is the data loss M4 exists to prevent, so a canonical-only capture — or an unset
// fidelity, which is absent evidence — selects nothing. The pipeline refuses earlier too; this is
// the second lock, because Select is exported and a future caller may reach it directly.
func TestAnUnrecoverableCaptureBacksNoRepresentation(t *testing.T) {
	for _, f := range []admission.Fidelity{admission.FidelityCanonical, ""} {
		sel := admission.Select(meaning(), f, admission.Baseline{})

		require.Equal(t, admission.FormNone, sel.Form,
			"fidelity %q cannot return the original, so nothing may point at it", f)
		require.Equal(t, admission.ReasonNoRepresentation, sel.Reason)
	}
}

// TestAbsentBaselineSelectsASelfContainedCapsule is invariant 8's default.
//
// With no baseline there is nothing to be relative to, and the answer is a capsule rather than a
// refusal: a capsule is always available, which is exactly why it is the form every doubt falls
// back to.
func TestAbsentBaselineSelectsASelfContainedCapsule(t *testing.T) {
	sel := admission.Select(meaning(), admission.FidelityExact, admission.Baseline{})

	require.Equal(t, admission.FormCapsule, sel.Form)
	require.Equal(t, admission.ResetNoBaseline, sel.Reset)
	require.True(t, sel.Form.Emits())
}

// TestAPriorDeliveryIsNotAVerifiedBaseline is T21-BASELINE-01's headline, and the rule Qompack.md
// states directly: "A same-epoch prior delivery is not proof that a relative delta has a valid
// baseline."
//
// The tempting implementation takes the previous delivery's identity off a session record and
// treats its existence as the baseline. That record proves something was delivered; it does not
// prove those bytes are still resolvable and still readable, which is what a delta needs. So a
// Baseline built as a composite literal — the only shape that shortcut can produce, because
// verification is unexported — is unverified by construction and resets to a capsule.
func TestAPriorDeliveryIsNotAVerifiedBaseline(t *testing.T) {
	var zero admission.Baseline
	require.False(t, zero.Verified(), "the zero Baseline must not claim verification")

	prior := admission.Baseline{ID: "toolresult:previous-delivery-this-epoch"}
	require.False(t, prior.Verified(),
		"a baseline assembled from a prior delivery record has not been read back")

	sel := admission.Select(meaning(), admission.FidelityExact, prior)

	require.Equal(t, admission.FormCapsule, sel.Form,
		"an unverified baseline resets to a self-contained capsule")
	require.Equal(t, admission.ResetUnverified, sel.Reset)
}

// TestAVerifiedCompatibleBaselineAdmitsADelta is the single positive case, and it is deliberately
// the only one: everything else in T21-BASELINE-01 is a reset.
func TestAVerifiedCompatibleBaselineAdmitsADelta(t *testing.T) {
	m := meaning()
	base := admission.VerifyBaseline("toolresult:prior", m.Signature, m.Schema, admission.FidelityFull)
	require.True(t, base.Verified())

	sel := admission.Select(m, admission.FidelityExact, base)

	require.Equal(t, admission.FormDelta, sel.Form)
	require.Equal(t, admission.ResetNone, sel.Reset)
	require.Equal(t, "toolresult:prior", sel.Base, "a delta must name the baseline it is against")
}

// TestACapsuleNamesNoBaseline is what "self-contained" means in checkable form.
//
// A capsule that carried a base would not be readable on its own, which defeats the reason
// invariant 8 puts capsules first.
func TestACapsuleNamesNoBaseline(t *testing.T) {
	base := admission.VerifyBaseline("toolresult:prior", "other-signature",
		admission.Target{Schema: "qompack.retrieval", Version: "1"}, admission.FidelityFull)

	sel := admission.Select(meaning(), admission.FidelityExact, base)

	require.Equal(t, admission.FormCapsule, sel.Form)
	require.Empty(t, sel.Base, "a capsule is self-contained and names no baseline")
}

// TestBaselineResetsAreReportedDistinctly is invariant 9, and the plan's required artifact: a
// "baseline-verification record with absent/corrupt/changed-signature resets".
//
// Every case here selects a capsule, so the form alone proves nothing about which rule fired.
// Recording the cause is what makes the record auditable — an operator who sees every delta
// resetting on ResetSchemaChanged learns something a bare "capsule" never tells them.
func TestBaselineResetsAreReportedDistinctly(t *testing.T) {
	m := meaning()
	compatible := func() admission.Baseline {
		return admission.VerifyBaseline("toolresult:prior", m.Signature, m.Schema, admission.FidelityFull)
	}

	for name, tc := range map[string]struct {
		base admission.Baseline
		want admission.ResetCause
	}{
		"absent": {
			base: admission.Baseline{},
			want: admission.ResetNoBaseline,
		},
		"corrupt, so never verified": {
			base: admission.Baseline{ID: "toolresult:prior"},
			want: admission.ResetUnverified,
		},
		"changed failure signature": {
			base: admission.VerifyBaseline("toolresult:prior", "build-failed:syntax",
				m.Schema, admission.FidelityFull),
			want: admission.ResetSignatureChanged,
		},
		"changed schema version": {
			base: admission.VerifyBaseline("toolresult:prior", m.Signature,
				admission.Target{Schema: m.Schema.Schema, Version: "2"}, admission.FidelityFull),
			want: admission.ResetSchemaChanged,
		},
		"changed schema name": {
			base: admission.VerifyBaseline("toolresult:prior", m.Signature,
				admission.Target{Schema: "qompack.other", Version: m.Schema.Version},
				admission.FidelityFull),
			want: admission.ResetSchemaChanged,
		},
		"uncertain baseline capture": {
			base: admission.VerifyBaseline("toolresult:prior", m.Signature, m.Schema,
				admission.FidelityCanonical),
			want: admission.ResetUncertainCapture,
		},
	} {
		t.Run(name, func(t *testing.T) {
			sel := admission.Select(m, admission.FidelityExact, tc.base)

			require.Equal(t, admission.FormCapsule, sel.Form,
				"every baseline doubt resets to a capsule, never to a refusal")
			require.Equal(t, tc.want, sel.Reset)
			require.Empty(t, sel.Base)
		})
	}

	// The control for the table above: the same call with a compatible baseline reaches a delta,
	// so each row's capsule is caused by its own mutation and not by the fixture.
	require.Equal(t, admission.FormDelta,
		admission.Select(m, admission.FidelityExact, compatible()).Form)
}

// TestAnUncertainCurrentCaptureAlsoResets is invariant 9's third clause read the other way round.
//
// The rule names "uncertain capture" without saying whose. A delta is a claim about the difference
// between two recoverable originals, so it needs both ends. The current side is covered by
// TestAnUnrecoverableCaptureBacksNoRepresentation for the unrecoverable case; this pins the
// weaker-but-still-recoverable one, where a full-fidelity current delivery against a full-fidelity
// baseline is still a legitimate delta.
func TestAnUncertainCurrentCaptureAlsoResets(t *testing.T) {
	m := meaning()
	base := admission.VerifyBaseline("toolresult:prior", m.Signature, m.Schema, admission.FidelityFull)

	sel := admission.Select(m, admission.FidelityFull, base)

	require.Equal(t, admission.FormDelta, sel.Form,
		"full fidelity is recoverable, so it is a valid end of a delta")
	require.Equal(t, admission.ResetNone, sel.Reset)
}

// TestSpanSurvivesAndUnknownStaysUnknown covers the "where available" half of the span rule.
//
// An absent span must stay absent rather than becoming a zero-length span at offset 0, which reads
// as "the declaration is at the head of the file" — a false anchor is worse than a missing one.
func TestSpanSurvivesAndUnknownStaysUnknown(t *testing.T) {
	require.True(t, admission.Span{Start: 4096, End: 8192}.Known())
	require.False(t, admission.Span{}.Known(), "the zero span is unknown, not a span at offset 0")
	require.False(t, admission.Span{Start: 10, End: 10}.Known(), "an empty range anchors nothing")
	require.False(t, admission.Span{Start: 20, End: 10}.Known(), "an inverted range is not a span")

	known := meaning()
	unknown := meaning()
	unknown.Source.Span = admission.Span{}

	require.Equal(t, []string{"Source"}, admission.Preserves(known, unknown),
		"dropping a span must be reported as a lost field, not tolerated")
}

// TestFormAndResetCauseRenderDistinctNames covers the String methods through the same exhaustive
// walk the Reason enum uses, so a form or reset cause added later cannot land untested.
func TestFormAndResetCauseRenderDistinctNames(t *testing.T) {
	t.Run("form", func(t *testing.T) {
		assertEnumIsExhaustive(t, "unknown", []string{
			admission.FormNone.String(),
			admission.FormCapsule.String(),
			admission.FormDelta.String(),
		}, func(i int) string { return admission.Form(i).String() })
	})

	t.Run("reset", func(t *testing.T) {
		assertEnumIsExhaustive(t, "unknown", []string{
			admission.ResetNone.String(),
			admission.ResetNoBaseline.String(),
			admission.ResetUnverified.String(),
			admission.ResetSignatureChanged.String(),
			admission.ResetSchemaChanged.String(),
			admission.ResetUncertainCapture.String(),
		}, func(i int) string { return admission.ResetCause(i).String() })
	})
}

// mutateField changes v in place to a value distinguishable from what it held, reporting whether
// it could. It exists so TestEveryMeaningFieldIsPreserved needs no per-field maintenance: a field
// added to Meaning is covered the moment it lands, or the helper reports it cannot alter it and
// the test fails loudly rather than skipping the field.
func mutateField(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.String:
		v.SetString(v.String() + "-changed")
		return true
	case reflect.Bool:
		v.SetBool(!v.Bool())
		return true
	case reflect.Int, reflect.Int64:
		v.SetInt(v.Int() + 1)
		return true
	case reflect.Slice:
		v.Set(reflect.Append(v, reflect.Zero(v.Type().Elem())))
		return true
	case reflect.Struct:
		for i := range v.NumField() {
			if mutateField(v.Field(i)) {
				return true
			}
		}
		return false
	default:
		return false
	}
}

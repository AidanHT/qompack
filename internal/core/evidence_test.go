package core_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
)

func TestObservationIdentityUsesPersistedArrival(t *testing.T) {
	first, err := core.NewObservationID("session", 1)
	require.NoError(t, err)
	replayed, err := core.NewObservationID("session", 1)
	require.NoError(t, err)
	second, err := core.NewObservationID("session", 2)
	require.NoError(t, err)
	other, err := core.NewObservationID("other", 1)
	require.NoError(t, err)
	require.Equal(t, first, replayed, "a restarted lease retains its observation identity")
	require.NotEqual(t, first, second, "equal payloads from separate deliveries remain distinct")
	require.NotEqual(t, first, other)
	_, err = core.NewObservationID("session", 0)
	require.Error(t, err, "an unassigned arrival must not mint an identity")
	_, err = core.NewObservationID("", 1)
	require.NoError(t, err, "an absent host session must remain absent, not fabricated")
}

func TestEvidenceEnvelopeLegacyAndFutureAreUnknown(t *testing.T) {
	for _, raw := range []string{`{}`, `{"v":99,"fidelity":"exact","coverage":"native_load_observed","outcome":"ok"}`} {
		var envelope core.EvidenceEnvelope
		require.NoError(t, json.Unmarshal([]byte(raw), &envelope))
		safe := envelope.Qualified()
		require.Equal(t, core.FidelityUnknown, safe.Fidelity)
		require.Equal(t, core.CoverageUnknown, safe.Coverage)
		require.Equal(t, core.OutcomeUncertain, safe.Outcome)
	}
}

func TestEvidenceEnvelopePreservesQualifiedFailure(t *testing.T) {
	for _, outcome := range []core.EvidenceOutcome{core.OutcomeDenied, core.OutcomeUnavailable, core.OutcomeCorrupt, core.OutcomeExpired} {
		original := core.EvidenceEnvelope{
			Version: core.EvidenceVersion, Fidelity: core.FidelityRedacted,
			Coverage: core.CoverageArchiveOnly, Outcome: outcome,
			Omissions: []core.Omission{{Reason: "privacy policy omitted original", Recovery: "request an authorized source"}},
		}
		encoded, err := json.Marshal(original)
		require.NoError(t, err)
		var decoded core.EvidenceEnvelope
		require.NoError(t, json.Unmarshal(encoded, &decoded))
		require.Equal(t, original, decoded.Qualified())
	}
}

func TestEvidenceEnvelopeCannotQualifyUnsupportedSuccessOrAbsence(t *testing.T) {
	for _, raw := range []string{
		`{"v":1,"fidelity":"future","coverage":"qompack_included","outcome":"ok"}`,
		`{"v":1,"fidelity":"exact","coverage":"future","outcome":"ok"}`,
		`{"v":1,"fidelity":"exact","coverage":"archive_only","outcome":"future"}`,
		`{"v":1,"fidelity":"unknown","coverage":"unknown","outcome":"absent"}`,
		`{"v":1,"fidelity":"exact","coverage":"archive_only","outcome":"absent","validity":{"generation":1}}`,
		`{"v":1,"fidelity":"exact","coverage":"qompack_included","outcome":"ok","validity":{"from":10,"to":5,"generation":1}}`,
	} {
		var envelope core.EvidenceEnvelope
		require.NoError(t, json.Unmarshal([]byte(raw), &envelope))
		require.Equal(t, core.OutcomeUncertain, envelope.Qualified().Outcome, raw)
	}
	valid := core.EvidenceEnvelope{
		Version: core.EvidenceVersion, Fidelity: core.FidelityUnknown,
		Coverage: core.CoverageArchiveOnly, Outcome: core.OutcomeOK,
	}
	require.Equal(t, valid, valid.Qualified(), "a known unknown fidelity is compatible with a successful archive lookup")
	require.False(t, core.Authority("").Valid())
	require.False(t, core.Authority("future_authority").Valid())
	require.True(t, core.AuthorityConflict.Valid())
	require.True(t, core.AuthorityUserCorrection.Valid())
}

// TestFidelity_ValidCoversTheCompleteDeclaredSet pins that every fidelity kind M1-01 requires is
// both declared and accepted, and that a future or misspelled spelling is not.
func TestFidelity_ValidCoversTheCompleteDeclaredSet(t *testing.T) {
	for _, f := range []core.Fidelity{
		core.FidelityExact, core.FidelityPrefix, core.FidelityPartial, core.FidelityRedacted,
		core.FidelityTruncated, core.FidelityBinary, core.FidelityFailure, core.FidelityUnknown,
	} {
		require.True(t, f.Valid(), "%q is a declared fidelity", f)
	}
	for _, f := range []core.Fidelity{"", "Exact", "complete", "partial_read"} {
		require.False(t, f.Valid(), "%q must not read as a declared fidelity", f)
	}
}

// TestCaptureError_ValidIsAClosedLabelSet pins the degradation labels a capture may record. The
// empty label is valid and means "nothing recorded", which is deliberately distinct from proof
// that a capture was complete.
func TestCaptureError_ValidIsAClosedLabelSet(t *testing.T) {
	for _, e := range []core.CaptureError{
		core.CaptureErrorNone, core.CaptureErrorOversize, core.CaptureErrorIncomplete,
		core.CaptureErrorNotJSON, core.CaptureErrorPolicy, core.CaptureErrorContract,
	} {
		require.True(t, e.Valid(), "%q is a declared capture error", e)
	}
	require.False(t, core.CaptureError("truncated").Valid())
	require.False(t, core.CaptureError("OVERSIZE").Valid())
}

// TestCaptureErrorLabelsCarryNoPayload pins that the labels themselves are constants an operator
// can read: none is built from, or able to leak, the bytes that produced it.
func TestCaptureErrorLabelsCarryNoPayload(t *testing.T) {
	require.Equal(t, core.CaptureError("oversize"), core.CaptureErrorOversize)
	require.Equal(t, core.CaptureError("incomplete_read"), core.CaptureErrorIncomplete)
	require.Equal(t, core.CaptureError("not_json_object"), core.CaptureErrorNotJSON)
	require.Equal(t, core.CaptureError("policy_unavailable"), core.CaptureErrorPolicy)
	require.Equal(t, core.CaptureError("policy_contract"), core.CaptureErrorContract)
	require.Equal(t, core.CaptureError(""), core.CaptureErrorNone)
}

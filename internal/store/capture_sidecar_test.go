package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
)

func testObservationID(t *testing.T, session core.SessionID, arrival uint64) core.ObservationID {
	t.Helper()
	id, err := core.NewObservationID(session, arrival)
	require.NoError(t, err)
	return id
}

// TestCaptureSidecar_RoundTripsThePermittedPayload is T20-M1-01: the raw host payload is persisted
// before normalization, keyed by observation identity, and comes back byte for byte.
func TestCaptureSidecar_RoundTripsThePermittedPayload(t *testing.T) {
	root := t.TempDir()
	id := testObservationID(t, "sess-1", 1)
	payload := []byte(`{"hook_event_name":"PostToolUse","tool_response":"ok","vendor_extra":{"a":1}}`)

	require.NoError(t, WriteCaptureSidecar(root, CaptureSidecar{
		ObservationID: id, Session: "sess-1", Arrival: 1, Op: "observe.tool",
		SourceFormat: "application/json", PolicyVersion: "redact-json/v1",
		HashVersion: core.EvidenceHashVersion, Fidelity: core.FidelityExact,
		Outcome: core.OutcomeOK, SourceBytes: len(payload), Bytes: payload,
		HostFields: []string{"hook_event_name", "tool_response", "vendor_extra"},
	}))

	got, err := ReadCaptureSidecar(root, id)
	require.NoError(t, err)
	require.Equal(t, CaptureSidecarVersion, got.Version)
	require.Equal(t, payload, got.Bytes, "the permitted payload is retained verbatim")
	require.Equal(t, core.FidelityExact, got.Fidelity)
	require.Equal(t, []string{"hook_event_name", "tool_response", "vendor_extra"}, got.HostFields,
		"an absent host field stays absent because the observed key list is recorded")
	require.False(t, got.BytesHash.IsZero())
	require.False(t, got.Published, "a capture with no reference is not published")
}

// TestCaptureSidecar_PreservesUnknownFieldsOnRoundTrip: a newer writer's key survives a
// read/modify/write by this build. That is the property the frozen ToolUseRecord wire shape cannot
// offer and the reason this record exists beside it.
func TestCaptureSidecar_PreservesUnknownFieldsOnRoundTrip(t *testing.T) {
	root := t.TempDir()
	id := testObservationID(t, "sess-2", 1)
	require.NoError(t, WriteCaptureSidecar(root, CaptureSidecar{
		ObservationID: id, Session: "sess-2", Arrival: 1, Outcome: core.OutcomeOK,
		Fidelity: core.FidelityExact, Bytes: []byte(`{"a":1}`),
	}))

	p, err := CaptureSidecarPath(root, id)
	require.NoError(t, err)
	raw, err := os.ReadFile(p)
	require.NoError(t, err)
	var fields map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &fields))
	fields["future_field"] = json.RawMessage(`{"kept":true}`)
	fields["v"] = json.RawMessage("2")
	rewritten, err := json.Marshal(fields)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(p, rewritten, 0o600))

	got, err := ReadCaptureSidecar(root, id)
	require.ErrorIs(t, err, core.ErrDegraded, "a newer record is readable, and says it is newer")
	require.Contains(t, got.Unknown, "future_field")

	got.Version = CaptureSidecarVersion
	back, err := json.Marshal(got)
	require.NoError(t, err)
	require.Contains(t, string(back), `"future_field"`,
		"a field this build does not name must survive being rewritten by it")
}

// TestCaptureSidecar_RegistersARetentionRoot is invariant 9 from this side: evidence declares
// itself to GC rather than relying on living outside objects/.
func TestCaptureSidecar_RegistersARetentionRoot(t *testing.T) {
	root := t.TempDir()
	id := testObservationID(t, "sess-3", 1)
	require.NoError(t, WriteCaptureSidecar(root, CaptureSidecar{
		ObservationID: id, Session: "sess-3", Arrival: 1, Outcome: core.OutcomeOK,
		Fidelity: core.FidelityExact, Bytes: []byte(`{"a":1}`),
	}))

	declared, err := os.ReadFile(RetentionRootsPath(root))
	require.NoError(t, err)
	got, err := ReadCaptureSidecar(root, id)
	require.NoError(t, err)
	require.Contains(t, string(declared), got.BytesHash.String())
	require.Contains(t, string(declared), string(RetentionEvidence))
	require.Contains(t, string(declared), string(id))
}

// TestCaptureSidecar_LinkIsTheVerifiedReferenceStage: the reference join is written into the record
// the capture already made durable, is idempotent, and refuses a sidecar that does not exist.
func TestCaptureSidecar_LinkIsTheVerifiedReferenceStage(t *testing.T) {
	root := t.TempDir()
	id := testObservationID(t, "sess-4", 1)
	ref := CaptureReference{ToolUseID: "tu_1", Root: core.HashBytes("test", []byte("root"))}

	require.Error(t, LinkCaptureReference(root, id, ref),
		"a reference may not name a capture this store cannot produce")

	require.NoError(t, WriteCaptureSidecar(root, CaptureSidecar{
		ObservationID: id, Session: "sess-4", Arrival: 1, Outcome: core.OutcomeOK,
		Fidelity: core.FidelityExact, Bytes: []byte(`{"a":1}`),
	}))
	require.NoError(t, LinkCaptureReference(root, id, ref))
	require.NoError(t, LinkCaptureReference(root, id, ref), "linking twice is the retry case")

	got, err := ReadCaptureSidecar(root, id)
	require.NoError(t, err)
	require.True(t, got.Published)
	require.Equal(t, core.ToolUseID("tu_1"), got.ToolUseID)
	require.Equal(t, ref.Root, got.Root)

	// A redelivery re-captures the same bytes; it must not un-publish what was already published.
	require.NoError(t, WriteCaptureSidecar(root, CaptureSidecar{
		ObservationID: id, Session: "sess-4", Arrival: 1, Outcome: core.OutcomeOK,
		Fidelity: core.FidelityExact, Bytes: []byte(`{"a":1}`),
	}))
	got, err = ReadCaptureSidecar(root, id)
	require.NoError(t, err)
	require.True(t, got.Published, "a redelivery does not forget a completed publication")
	require.Equal(t, core.ToolUseID("tu_1"), got.ToolUseID)
}

// TestCaptureSidecar_RefusesAnIdentityThatIsNotADigest: the identity becomes a path element.
func TestCaptureSidecar_RefusesAnIdentityThatIsNotADigest(t *testing.T) {
	root := t.TempDir()
	for _, bad := range []core.ObservationID{
		"", "../../escape", core.ObservationID("sha256:" + strings.Repeat("Z", 64)), core.ObservationID(strings.Repeat("a", 63)),
	} {
		require.Error(t, WriteCaptureSidecar(root, CaptureSidecar{ObservationID: bad}),
			"identity %q must not name a file", bad)
	}
	require.NoDirExists(t, filepath.Join(paths.Of(root).Records, captureSidecarDir, ".."))
}

// TestCaptureSidecar_DegradedCaptureIsRecordedAsSuch is T20-M1-02: a delivery that could not be
// captured whole still leaves a measurable, distinguishable trace.
func TestCaptureSidecar_DegradedCaptureIsRecordedAsSuch(t *testing.T) {
	root := t.TempDir()
	id := testObservationID(t, "sess-5", 1)
	require.NoError(t, WriteCaptureSidecar(root, CaptureSidecar{
		ObservationID: id, Session: "sess-5", Arrival: 1,
		Fidelity: core.FidelityTruncated, Outcome: core.OutcomeUnavailable,
		CaptureError: core.CaptureErrorOversize, Truncated: true, SourceBytes: 1 << 20,
	}))
	got, err := ReadCaptureSidecar(root, id)
	require.NoError(t, err)
	require.Equal(t, core.CaptureErrorOversize, got.CaptureError)
	require.Equal(t, core.FidelityTruncated, got.Fidelity)
	require.Equal(t, core.OutcomeUnavailable, got.Outcome)
	require.Equal(t, 1<<20, got.SourceBytes, "a refused delivery is still distinguishable from none")
	require.Empty(t, got.Bytes)
	require.True(t, got.BytesHash.IsZero())

	// Nothing was retained, so nothing is declared: an empty capture is not an evidence root.
	_, err = os.ReadFile(RetentionRootsPath(root))
	require.True(t, os.IsNotExist(err), "an empty capture declares no retention root")
}

package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/paths"
)

func TestDispatchOp_OutOfProjectDirectIPCDoesNotReachWAL(t *testing.T) {
	root := t.TempDir()
	dd, calls := newObservingDaemon(t, root)
	req := observeRequest(testDeliveryToken('4'), "outside-direct", `{"hook_event_name":"PostToolUse"}`)
	req.Capture = nil
	req.Event.ToolName = "Read"
	req.Event.ToolInput = outOfProjectInput(t)
	resp := dd.dispatchOp(context.Background(), req)
	require.True(t, resp.OK, "a terminal privacy refusal acknowledges the transport")
	require.Contains(t, string(resp.Data), string(core.OutcomeDenied))
	require.Zero(t, calls())
	require.Empty(t, dd.ing.ring)
	entries, err := os.ReadDir(paths.Of(root).Spool)
	if !os.IsNotExist(err) {
		require.NoError(t, err)
	}
	require.Empty(t, entries, "admission precedes the route that appends the WAL")
}

// V6-AUTH-1 at the daemon admission gate. The daemon must enforce path scope over EVERY byte source
// a supplied decision would persist — the retained Event, the already-redacted Capture.Bytes, and
// the Raw extras — even when the request already claims OutcomeOK, and including the degraded branch.
// A proven escape is Denied; a source whose containment cannot be proven is Failed (unavailable).

func outOfProjectInput(t *testing.T) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(map[string]any{"file_path": filepath.Join(t.TempDir(), "secret.txt")})
	require.NoError(t, err)
	return b
}

func outOfProjectPayload(t *testing.T, tool string) []byte {
	t.Helper()
	b, err := json.Marshal(map[string]any{
		"tool_name":  tool,
		"tool_input": map[string]any{"file_path": filepath.Join(t.TempDir(), "secret.txt")},
	})
	require.NoError(t, err)
	return b
}

func TestAdmitDelivery_ForgedOKCaptureWithOutOfProjectPathDenied(t *testing.T) {
	root := t.TempDir()
	dd, _ := newObservingDaemon(t, root)

	req := observeRequest(testDeliveryToken('a'), "sess-forge", `{"hook_event_name":"PostToolUse"}`)
	req.Event.ToolName = "Read"
	req.Event.ToolInput = outOfProjectInput(t)

	v := dd.admitDelivery(req)
	require.True(t, v.Denied, "an OK capture is not trusted to be in scope")
	require.False(t, v.Failed)
	require.False(t, v.Degraded)
	require.Contains(t, v.Reason, "out of project")
	require.Greater(t, dd.m.Counter(counterAdmissionScopeDenied).Value(), int64(0),
		"a scope denial is counted apart from a policy denial")
}

func TestAdmitDelivery_ForgedOKInProjectEventButOutOfProjectBytesDenied(t *testing.T) {
	root := t.TempDir()
	dd, _ := newObservingDaemon(t, root)

	// The Event names an in-project path, but the already-redacted Capture.Bytes — what the sidecar
	// actually persists — names an out-of-project one. Scoping only the Event would leave this a
	// durable leak (authority-review §5). Parsing the redacted bytes reintroduces nothing.
	req := observeRequest(testDeliveryToken('b'), "sess-bytes", `{"hook_event_name":"PostToolUse"}`)
	req.Event.ToolName = "Read"
	req.Event.ToolInput = json.RawMessage(`{"file_path":"src/auth.go"}`)
	req.Capture.Bytes = outOfProjectPayload(t, "Read")

	v := dd.admitDelivery(req)
	require.True(t, v.Denied, "the persisted Capture.Bytes must be scoped, not just the Event")
	require.Contains(t, v.Reason, "out of project")
	require.Greater(t, dd.m.Counter(counterAdmissionScopeDenied).Value(), int64(0))
}

func TestAdmitDelivery_OKCaptureWithDroppedPathUnavailable(t *testing.T) {
	root := t.TempDir()
	dd, _ := newObservingDaemon(t, root)

	// A file producer whose path was dropped cannot be proven inside: refuse as UNAVAILABLE (a gap),
	// not Denied (a proven escape) and not a false absence.
	req := observeRequest(testDeliveryToken('f'), "sess-drop", `{"hook_event_name":"PostToolUse"}`)
	req.Event.ToolName = "Read"
	req.Event.ToolInput = json.RawMessage(`{}`)

	v := dd.admitDelivery(req)
	require.False(t, v.Denied, "an unprovable path is not a proven escape")
	require.True(t, v.Failed, "it is unavailable: a gap, not an absence")
	require.Contains(t, v.Reason, "unprovable")
	require.Greater(t, dd.m.Counter(counterAdmissionScopeUnavailable).Value(), int64(0))
}

func TestAdmitDelivery_DegradedWithOutOfProjectBytesRefused(t *testing.T) {
	root := t.TempDir()
	dd, _ := newObservingDaemon(t, root)

	// A degraded (evidence) decision whose retained prefix carries an out-of-project path must not
	// be admitted as evidence — the degraded branch used to skip scope entirely.
	req := observeRequest(testDeliveryToken('g'), "sess-degraded", `{"hook_event_name":"PostToolUse"}`)
	req.Event = nil
	req.Capture = &hookio.Capture{
		Version: core.EvidenceVersion, SourceFormat: "application/json",
		PolicyVersion: "redact-json/v1", HashVersion: core.EvidenceHashVersion,
		Fidelity: core.FidelityTruncated, Outcome: core.OutcomeUnavailable,
		CaptureError: core.CaptureErrorOversize, Truncated: true,
		Bytes: outOfProjectPayload(t, "Read"), SourceBytes: 5 << 20,
	}

	v := dd.admitDelivery(req)
	require.True(t, v.Denied, "an out-of-project degraded prefix is refused, not admitted as evidence")
	require.False(t, v.Degraded)
}

func TestAdmitDelivery_ByteFreeDegradedAdmittedAsEvidence(t *testing.T) {
	root := t.TempDir()
	dd, _ := newObservingDaemon(t, root)

	// A byte-free evidence record (the client already dropped any unprovable bytes) has nothing to
	// leak and is admitted as the degraded observation it is.
	req := observeRequest(testDeliveryToken('h'), "sess-evidence", `{"hook_event_name":"PostToolUse"}`)
	req.Event = nil
	req.Capture = &hookio.Capture{
		Version: core.EvidenceVersion, SourceFormat: "application/json",
		PolicyVersion: "redact-json/v1", HashVersion: core.EvidenceHashVersion,
		Fidelity: core.FidelityTruncated, Outcome: core.OutcomeUnavailable,
		CaptureError: core.CaptureErrorOversize, Truncated: true, SourceBytes: 5 << 20,
	}

	v := dd.admitDelivery(req)
	require.True(t, v.Degraded, "a byte-free evidence record is admitted")
	require.False(t, v.Denied)
	require.False(t, v.Failed)
}

func TestAdmitDelivery_OKCaptureInProjectAdmitted(t *testing.T) {
	root := t.TempDir()
	dd, _ := newObservingDaemon(t, root)

	req := observeRequest(testDeliveryToken('c'), "sess-ok", `{"hook_event_name":"PostToolUse"}`)
	req.Event.ToolName = "Read"
	req.Event.ToolInput = json.RawMessage(`{"file_path":"src/auth.go"}`)

	v := dd.admitDelivery(req)
	require.False(t, v.Denied, "a genuine in-project OK capture is admitted unchanged")
	require.False(t, v.Failed)
	require.False(t, v.Degraded)
}

func TestAdmitDelivery_PathlessBashOKCaptureAdmitted(t *testing.T) {
	root := t.TempDir()
	dd, _ := newObservingDaemon(t, root)

	req := observeRequest(testDeliveryToken('d'), "sess-bash", `{"hook_event_name":"PostToolUse"}`)
	req.Event.ToolName = "Bash"
	req.Event.ToolInput = json.RawMessage(`{"command":"go test ./..."}`)

	v := dd.admitDelivery(req)
	require.False(t, v.Denied, "a pathless producer is preserved")
	require.False(t, v.Failed)
}

func TestAdmitDelivery_NoCaptureOutOfProjectDeniedBeforePolicy(t *testing.T) {
	root := t.TempDir()
	dd, _ := newObservingDaemon(t, root)

	// A request that carries NO decision is admitted over its reconstructed payload — and scoped
	// first, so an out-of-project file capture is refused whether or not redaction would admit it.
	req := observeRequest(testDeliveryToken('e'), "sess-nocap", `{"hook_event_name":"PostToolUse"}`)
	req.Capture = nil
	req.Event.ToolName = "Read"
	req.Event.ToolInput = outOfProjectInput(t)

	v := dd.admitDelivery(req)
	require.True(t, v.Denied)
	require.Contains(t, v.Reason, "out of project")
}

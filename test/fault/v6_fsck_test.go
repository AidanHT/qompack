package fault

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/store"
)

// TestV6_FsckReportsUnpublishedCaptureWithoutDiscardingEvidence verifies SP17's
// named manual diagnostic on the packaged binary. It does not retire F4-4:
// automatic recovery/status still has to account for the incomplete capture.
func TestV6_FsckReportsUnpublishedCaptureWithoutDiscardingEvidence(t *testing.T) {
	b := assembledBundle(t)
	p := newProject(t, "proj")
	t.Cleanup(func() {
		shutdownIfReachable(t, p.Root)
		requireNoOrphan(t, p.Root)
	})
	const sess = core.SessionID("sess-v6-fsck-stage-one")
	seedSession(t, b, p, sess)
	shutdownIfReachable(t, p.Root)

	rec := newRecord(t, "v6_fsck_unpublished_capture")
	rec.Phase = PhasePublication
	rec.Boundary = "capture sidecar published:false with no reference joined"
	rec.SeedMethod = "packaged hook capture, stopped writer, then a published observe.tool sidecar returned to stage one"
	rec.Outcome = OutcomeFailed
	rec.Reason = "packaged fsck did not complete the expected diagnostic and preservation assertions"
	defer func() { writeRecord(t, rec) }()

	cut := v6UnpublishToolCapture(t, p.Root)
	rec.Detail = cut
	beforeObjects := objectFingerprints(t, p.Root)
	beforeSidecars := v6CaptureBytes(t, p.Root)

	stdout, stderr, code := run(t, b.Bin, p.Root,
		[]string{"fsck", "--project", p.Root, "--json"}, nil, p.Env)
	rec.Detail += fmt.Sprintf("; fsck exit=%d; report=%s", code, stdout)
	var report struct {
		ReadOnly bool `json:"read_only"`
		Checks   []struct {
			ID     string   `json:"id"`
			OK     bool     `json:"ok"`
			Count  int      `json:"count"`
			Detail []string `json:"detail"`
		} `json:"checks"`
	}
	require.NoError(t, json.Unmarshal(stdout, &report), "fsck must return structured diagnostics: %s", stderr)
	require.Equal(t, 1, code, "the incomplete capture must produce a failing diagnostic")
	require.True(t, report.ReadOnly)
	found := false
	for _, row := range report.Checks {
		if row.ID == "captures" {
			found = !row.OK && row.Count > 0 && strings.Contains(strings.Join(row.Detail, "\n"), "stage 1 only")
		}
	}
	require.True(t, found, "fsck must identify this publication boundary")
	require.Equal(t, beforeObjects, objectFingerprints(t, p.Root), "audit must preserve stored objects")
	require.Equal(t, beforeSidecars, v6CaptureBytes(t, p.Root), "audit must preserve capture evidence")
	rec.Outcome = OutcomeExplicitIncomplete
	rec.Reason = "operator-invoked packaged fsck reports the stage-one capture and exits 1 without changing its objects or sidecars; this is not automatic recovery"
	rec.Detail = fmt.Sprintf("%s; fsck read_only=true; objects=%d; sidecars=%d", cut, len(beforeObjects), len(beforeSidecars))
}

// Select the operation, not a filename order: prompt sidecars are deliberately
// unpublished and do not represent a failed tool-reference publication.
func v6UnpublishToolCapture(t *testing.T, root string) string {
	t.Helper()
	for _, path := range sidecarPaths(root) {
		raw, err := os.ReadFile(longPath(path))
		require.NoError(t, err)
		var sc store.CaptureSidecar
		require.NoError(t, json.Unmarshal(raw, &sc))
		if sc.Op != string(ipc.OpObserveTool) || sc.Outcome != core.OutcomeOK || !sc.Published || sc.BytesHash.IsZero() {
			continue
		}
		sc.Published, sc.Root, sc.ToolUseID = false, core.Hash{}, ""
		changed, err := json.Marshal(sc)
		require.NoError(t, err)
		writeOver(t, path, changed)
		return "returned observe.tool capture " + string(sc.ObservationID) + " to stage one"
	}
	t.Fatal("seed produced no published tool capture with durable bytes")
	return ""
}

func v6CaptureBytes(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, path := range sidecarPaths(root) {
		b, err := os.ReadFile(longPath(path))
		require.NoError(t, err)
		out[path] = string(b)
	}
	require.NotEmpty(t, out)
	return out
}

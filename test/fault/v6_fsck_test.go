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

// TestV6_StartupAccountingReportsUnpublishedCaptureViaStatus is the AUTOMATIC half of
// V6-RECOVERY-1 that the manual fsck test above is not: it proves the daemon's own startup
// publication accounting — the pass main wires after the drain and sweepCheckpointIntegrity, under a
// 250ms bound — detects a genuine observe.tool capture stuck at stage one and reports it explicitly
// through `status --json` (the daemon's loud tail and its publication counters), while changing not
// one byte of the object store or the sidecar.
//
// It deliberately does NOT retire F4-4 either: detection is not recovery. The capture is still
// unpublished afterwards, this scan repairs nothing, and reapplying a sidecar without the current
// authority is main's ruling to leave to the operator's verified backup/restore. What this row adds
// over the manual fsck is that the product now SURFACES the gap on its own at startup, with no
// operator command, which is the automatic-accounting obligation V6-RECOVERY-1 named as missing.
func TestV6_StartupAccountingReportsUnpublishedCaptureViaStatus(t *testing.T) {
	b := assembledBundle(t)
	p := newProject(t, "proj")
	t.Cleanup(func() {
		shutdownIfReachable(t, p.Root)
		requireNoOrphan(t, p.Root)
	})
	const sess = core.SessionID("sess-v6-startup-stage-one")
	seedSession(t, b, p, sess)
	shutdownIfReachable(t, p.Root)

	rec := newRecord(t, "v6_startup_accounting_unpublished_capture")
	rec.Phase = PhasePublication
	rec.Boundary = "capture sidecar published:false with no reference joined, discovered at daemon startup"
	rec.SeedMethod = "packaged hook capture, stopped writer, then a published observe.tool sidecar returned to stage one; the daemon is then restarted so its startup accounting runs"
	rec.Outcome = OutcomeFailed
	rec.Reason = "startup accounting did not surface the stage-one capture on status, or did not preserve the evidence"
	defer func() { writeRecord(t, rec) }()

	cut := v6UnpublishToolCapture(t, p.Root)
	beforeObjects := objectFingerprints(t, p.Root)
	beforeSidecars := v6CaptureBytes(t, p.Root)

	// Restart the daemon so its Run reaches the startup accounting the composition root wires in.
	// A bare session-start brings the detached daemon up without observing anything, so the only
	// publication gap it can find is the one the cut left.
	const restart = core.SessionID("sess-v6-startup-stage-one-restart")
	runHook(t, b.Bin, p, []string{"session-start"}, sessionStartPayload(t, p.Root, restart, "startup"))
	if !waitDaemonUp(t, p.Root) {
		t.Fatalf("fault: the restart session-start did not bring a daemon up for %s", p.Root)
	}

	// status --json, asked WITH THE DAEMON UP so StatusReport.Snapshot is the daemon's own — its
	// loud tail and its counters, which is where the startup accounting's report lives.
	statusOut, statusErr, _ := run(t, b.Bin, p.Root, []string{"status", "--json"}, nil, p.Env)
	var env statusEnvelope
	require.NoError(t, json.Unmarshal(statusOut, &env),
		"status --json must return the slash-command envelope; stderr=%s", statusErr)
	require.NotNil(t, env.Data.Snapshot,
		"status --json must carry the daemon's snapshot when the daemon is up, or the counters and "+
			"loud tail the startup accounting reports through are unreadable")

	counterNamed := false
	for name, v := range env.Data.Snapshot.Counters {
		if v > 0 && strings.Contains(name, "publication") {
			counterNamed = true
			break
		}
	}
	loudTailNamed := false
	for _, l := range env.Data.Snapshot.LoudTail {
		if strings.Contains(l, "unpublished") {
			loudTailNamed = true
			break
		}
	}
	surfaced := counterNamed || loudTailNamed
	require.True(t, surfaced,
		"the daemon's startup accounting must name the stage-one capture on `status --json` — a "+
			"publication counter (counter=%v) or the loud tail (tail=%v)", counterNamed, loudTailNamed)

	// The same line is durable on LOUD.log (§13 invariant 10: degradation is loud and never rotated).
	loudNamed := false
	for _, l := range loudLines(p.Root) {
		if strings.Contains(l, "unpublished") {
			loudNamed = true
			break
		}
	}
	require.True(t, loudNamed, "the startup accounting must also leave a LOUD.log line naming the gap")

	// Detection is not recovery: the capture is still at stage one, and the read-only accounting
	// changed nothing it walked.
	require.True(t, hasUnpublishedToolCapture(t, p.Root),
		"the accounting must not have repaired the capture — a stage-one observe.tool sidecar must remain")
	require.Equal(t, beforeObjects, objectFingerprints(t, p.Root),
		"the read-only accounting must preserve every stored object byte for byte")
	require.Equal(t, beforeSidecars, v6CaptureBytes(t, p.Root),
		"the read-only accounting must preserve the capture evidence byte for byte")

	rec.Outcome = OutcomeExplicitIncomplete
	rec.Reason = "the daemon's startup accounting names the stage-one observe.tool capture on `status --json` " +
		"(loud tail / publication counter) and on LOUD.log, and preserves the objects and the sidecar unchanged; " +
		"the capture is still unpublished afterwards — this is automatic detection, not recovery, which stays the " +
		"operator's verified backup/restore path"
	rec.Detail = fmt.Sprintf("%s; status gaps naming the cut present=%v; objects=%d; sidecars=%d",
		cut, surfaced, len(beforeObjects), len(beforeSidecars))
}

// hasUnpublishedToolCapture reports whether any sidecar on disk is still a genuine stage-one gap by
// fsck's own criterion (an observe.tool delivery, outcome ok, durable bytes, unpublished). It is how
// the startup row proves the accounting DETECTED without REPAIRING.
func hasUnpublishedToolCapture(t *testing.T, root string) bool {
	t.Helper()
	for _, path := range sidecarPaths(root) {
		raw, err := os.ReadFile(longPath(path))
		if err != nil {
			continue
		}
		var sc store.CaptureSidecar
		if json.Unmarshal(raw, &sc) != nil {
			continue
		}
		if sc.Op == string(ipc.OpObserveTool) && sc.Outcome == core.OutcomeOK &&
			!sc.Published && !sc.BytesHash.IsZero() {
			return true
		}
	}
	return false
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

package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/store"
)

func TestFsck_UnknownObservationIntentIsIncompleteAndReadOnly(t *testing.T) {
	p := seedFsckProject(t)
	file := filepath.Join(p.Layot.Index, "observations.jsonl")
	require.NoError(t, os.WriteFile(file, []byte("{\"v\":999,\"unknown\":\"private-test-body\"}\n"), 0o600))
	before := snapshotQompack(t, p.Layot)
	code, doc, _ := fsckJSON(t, p.Root)
	require.NotEqual(t, ExitOK, code)
	row := fsckRequireRow(t, doc, "publication")
	require.Equal(t, false, row["ok"])
	require.Contains(t, fsckDetail(row), "incomplete")
	require.NotContains(t, fsckDetail(row), "private-test-body")
	require.Equal(t, before, snapshotQompack(t, p.Layot), "audit must preserve unknown intent evidence")
}

// fsckPlantNewerSidecar writes a capture sidecar declaring a schema newer than this build, at the
// path the sidecar layout gives it, and returns that path.
func fsckPlantNewerSidecar(t *testing.T, root, seed string) string {
	t.Helper()
	id := core.ObservationID(core.HashBytes("fsck.test.obs", []byte(seed)).String())
	path, err := store.CaptureSidecarPath(root, id)
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(paths.Long(filepath.Dir(path)), 0o700))
	require.NoError(t, os.WriteFile(paths.Long(path), []byte(fmt.Sprintf(
		`{"v":%d,"observation_id":%q,"op":"observe.tool","published":false,"outcome":"ok"}`+"\n",
		store.CaptureSidecarVersion+1, id)), 0o600))
	return path
}

// TestFsck_NewerCaptureSchemaIsASupportGapNotADefect: a sidecar written by a newer build is what the
// captures row already calls "a support gap rather than damage" (Qompack.md §7.1), so the publication
// row must not call the same file a defect. It still says, by name, that it could not certify those
// sidecars — the pass is not reported clean — and it writes nothing.
func TestFsck_NewerCaptureSchemaIsASupportGapNotADefect(t *testing.T) {
	p := seedFsckProject(t)
	fsckPlantNewerSidecar(t, p.Root, "newer")
	before := snapshotQompack(t, p.Layot)

	code, doc, _ := fsckJSON(t, p.Root)
	require.Equal(t, ExitOK, code, "a newer artifact is a support gap, not a defect: %v", doc)
	captures := fsckRequireRow(t, doc, "captures")
	require.Equal(t, true, captures["ok"])
	require.Contains(t, fsckDetail(captures), "newer than this build")
	row := fsckRequireRow(t, doc, "publication")
	require.Equal(t, true, row["ok"], "detail=%s", fsckDetail(row))
	require.Contains(t, fsckDetail(row), "written by a newer build",
		"the row must still say which sidecars it could not certify")
	require.Contains(t, fsckDetail(row), "capture sidecar schema newer than this build")
	require.Equal(t, before, snapshotQompack(t, p.Layot), "fsck writes nothing")
}

// TestFsck_LegacyControlCaptureIsNotADefect: builds before the V6 close-out published a capture
// sidecar for a drained control line (a session start, checkpoint or SessionEnd flush that had fallen
// back to its client spool). Such a file is evidence of a delivery, not an observation, and nothing
// ever references it. fsck must classify it as that known legacy artifact — in the captures row and
// in the publication row — rather than report "capture publication requirement is unknown" and an
// incomplete audit for the life of the project; and it must leave the file exactly where it is.
func TestFsck_LegacyControlCaptureIsNotADefect(t *testing.T) {
	p := seedFsckProject(t)
	for _, op := range []string{"flush", "checkpoint", "session.start"} {
		require.NoError(t, store.WriteCaptureSidecar(p.Root, store.CaptureSidecar{
			ObservationID: core.ObservationID(core.HashBytes("fsck.test.obs", []byte(op)).String()),
			Session:       "s-fsck",
			Op:            op,
			Outcome:       core.OutcomeOK,
			Bytes:         []byte(`{"hook_event_name":"SessionEnd"}`),
		}))
	}
	before := snapshotQompack(t, p.Layot)

	code, doc, _ := fsckJSON(t, p.Root)
	require.Equal(t, ExitOK, code, "a legacy control-line sidecar is not a defect: %v", doc)
	captures := fsckRequireRow(t, doc, "captures")
	require.Equal(t, true, captures["ok"], "detail=%s", fsckDetail(captures))
	require.NotContains(t, fsckDetail(captures), "requirement is unknown")
	require.Contains(t, fsckDetail(captures), "3 capture sidecar(s) record control lines")
	row := fsckRequireRow(t, doc, "publication")
	require.Equal(t, true, row["ok"], "detail=%s", fsckDetail(row))
	require.Contains(t, fsckDetail(row), "3 capture sidecar(s) record control lines")
	require.Equal(t, before, snapshotQompack(t, p.Layot), "fsck keeps the evidence and writes nothing")
}

// TestFsck_NewerCaptureSchemaDoesNotExcuseOtherIncompleteness: the support gap excuses exactly itself.
// Beside any other cause of an incomplete audit the row stays a defect.
func TestFsck_NewerCaptureSchemaDoesNotExcuseOtherIncompleteness(t *testing.T) {
	p := seedFsckProject(t)
	fsckPlantNewerSidecar(t, p.Root, "newer")
	stray := filepath.Join(p.Layot.Records, "captures", "stray.json")
	require.NoError(t, os.WriteFile(paths.Long(stray), []byte("{}\n"), 0o600))

	code, doc, _ := fsckJSON(t, p.Root)
	require.NotEqual(t, ExitOK, code)
	row := fsckRequireRow(t, doc, "publication")
	require.Equal(t, false, row["ok"])
	require.Contains(t, fsckDetail(row), "zero observed gaps cannot certify completeness")
	require.Contains(t, fsckDetail(row), "unexpected entry in the capture tree")
}

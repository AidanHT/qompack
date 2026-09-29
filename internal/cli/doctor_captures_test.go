package cli

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/store"
)

// seedPublishedToolSidecar writes the ordinary published state a live tool delivery ends in: its
// capture sidecar, joined to the tool_use record and the content root that record names.
func seedPublishedToolSidecar(t *testing.T, p seededProject) {
	t.Helper()
	id := core.ObservationID(strings.Repeat("cd", 32))
	require.NoError(t, store.WriteCaptureSidecar(p.Root, store.CaptureSidecar{
		ObservationID: id,
		Session:       core.SessionID("s-fsck"),
		Op:            string(ipc.OpObserveTool),
		Outcome:       core.OutcomeOK,
		Bytes:         []byte("package main\n\nfunc main() {}\n"),
	}))
	require.NoError(t, store.LinkCaptureReference(p.Root, id, store.CaptureReference{
		ToolUseID: core.ToolUseID("tu-fsck-1"), Root: fixtureRootHash(t, p),
	}))
}

// TestDoctor_UnpublishedCapturesAgreesWithFsck is the Phase 4 live lane's install D5: on a store
// whose every capture was published, doctor's captures.unpublished read degraded ("5 gap(s) across 7
// sidecar(s)") while fsck's captures and publication rows passed. Doctor ran fsck's captures walk
// without the index fsck loads before it, so every published sidecar's root looked unresolvable
// and counted as a gap. The row must count what fsck's publication audit counts, and nothing else.
func TestDoctor_UnpublishedCapturesAgreesWithFsck(t *testing.T) {
	p := seedFsckProject(t)
	seedPublishedToolSidecar(t, p)

	_, fsckDoc, _ := fsckJSON(t, p.Root)
	for _, id := range []string{"captures", "publication"} {
		row := fsckRequireRow(t, fsckDoc, id)
		require.Equal(t, true, row["ok"], "fixture: fsck %s passes: %s", id, fsckDetail(row))
	}

	_, doc, _ := doctorJSON(t, p.Root)
	row := doctorFindRow(t, doc, "recording", "captures.unpublished")
	require.Equal(t, "ok", row["status"], "doctor must agree with fsck on a fully published store: %v", row)
	require.Equal(t, "0 gap(s) across 1 sidecar(s)", row["observed"])

	seedStageOneSidecar(t, p)
	_, doc, _ = doctorJSON(t, p.Root)
	row = doctorFindRow(t, doc, "recording", "captures.unpublished")
	require.Equal(t, "degraded", row["status"], "a real stage-one gap is still reported: %v", row)
	require.Equal(t, "1 gap(s) across 2 sidecar(s)", row["observed"])
}

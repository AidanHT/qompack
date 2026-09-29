package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
)

// seedUnsealedClaim writes the state a build before the two-phase encode left after a clean idle
// exit (F-UAT03-2, live evidence uat/UAT-03/cli/s5-fsck-json): an encode record naming the
// sequence of a draft that no compaction sealed, and that draft's own state file holding the
// segment. name is the draft's file name under state/.
func seedUnsealedClaim(t *testing.T, p seededProject, name string, seq core.CheckpointSeq, seg core.SegmentID) {
	t.Helper()
	appendLine(t, filepath.Join(p.Layot.Index, "segments.jsonl"),
		fmt.Sprintf(`{"v":1,"op":"encode","id":%d,"seq":%d,"ts":1}`, seg, seq))
	draft := fmt.Sprintf(`{"session":"s-fsck","seq":%d,"parent":1,"frontier":7,"encoded":[%d],`+
		`"started":1,"checkpoint":{"version":1,"session":"s-fsck","seq":%d,"created":"","encoded_segments":[%d]}}`,
		seq, seg, seq, seg)
	require.NoError(t, os.WriteFile(paths.Long(filepath.Join(p.Layot.State, name)), []byte(draft), 0o600))
}

// TestFsck_AnUnsealedDraftClaimIsNamedNotFailed is the compatibility half of F-UAT03-2: a store
// that already carries the claim (the draft persisted, the session never compacted again) reads
// index.segments ok with the condition named, so fsck exits 0 and a backup of it restores.
func TestFsck_AnUnsealedDraftClaimIsNamedNotFailed(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name, file, says string
	}{
		{name: "a live draft", file: "draft-s-fsck.json", says: "next compaction seals it"},
		{name: "a set-aside draft", file: "draft-s-fsck.stale.json", says: "No checkpoint will carry this segment"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p := seedFsckProject(t)
			seedUnsealedClaim(t, p, tc.file, p.CPSeq+1, 7)

			code, doc, errw := fsckJSON(t, p.Root)
			row := fsckRequireRow(t, doc, "index.segments")
			require.Equal(t, true, row["ok"], "an unsealed-draft claim is not a defect: %s", fsckDetail(row))
			require.Contains(t, fsckDetail(row), fsckUnsealedDraftClaim)
			require.Contains(t, fsckDetail(row), tc.says)
			require.Contains(t, fsckDetail(row), tc.file)
			require.Equal(t, ExitOK, code, "stderr=%s", errw)
		})
	}
}

// TestFsck_AClaimNothingExplainsIsStillADefect keeps the check's teeth: a draft that holds a
// DIFFERENT segment, or the same segment for a different checkpoint, explains nothing.
func TestFsck_AClaimNothingExplainsIsStillADefect(t *testing.T) {
	t.Parallel()

	p := seedFsckProject(t)
	seedUnsealedClaim(t, p, "draft-s-fsck.json", p.CPSeq+1, 7)
	appendLine(t, filepath.Join(p.Layot.Index, "segments.jsonl"), `{"v":1,"op":"encode","id":8,"seq":2,"ts":1}`)
	appendLine(t, filepath.Join(p.Layot.Index, "segments.jsonl"), `{"v":1,"op":"encode","id":7,"seq":3,"ts":1}`)

	code, doc, _ := fsckJSON(t, p.Root)
	row := fsckRequireRow(t, doc, "index.segments")
	require.Equal(t, false, row["ok"])
	require.Contains(t, fsckDetail(row), "segment 8 records that it was encoded into checkpoint 0002")
	require.Contains(t, fsckDetail(row), "segment 7 records that it was encoded into checkpoint 0003")
	require.NotEqual(t, ExitOK, code)
}

// TestFsck_AClaimOnAnOrphanArtifactIsTheOrphansFinding: a cut between the seal's segment commit
// and its MANIFEST line leaves the artifact on disk; the checkpoints row owns that finding (and
// --repair), so index.segments names it without counting it twice.
func TestFsck_AClaimOnAnOrphanArtifactIsTheOrphansFinding(t *testing.T) {
	t.Parallel()

	p := seedFsckProject(t)
	b, err := checkpoint.Marshal(checkpoint.Checkpoint{
		Session: core.SessionID("s-fsck"), Seq: 9, Created: "2026-08-12T10:31:00.000Z",
	})
	require.NoError(t, err)
	require.NoError(t, paths.CreateNew(paths.CheckpointPath(p.Layot, 9), b))
	appendLine(t, filepath.Join(p.Layot.Index, "segments.jsonl"), `{"v":1,"op":"encode","id":7,"seq":9,"ts":1}`)

	_, doc, _ := fsckJSON(t, p.Root)
	row := fsckRequireRow(t, doc, "index.segments")
	require.Equal(t, true, row["ok"], fsckDetail(row))
	require.Contains(t, fsckDetail(row), "whose artifact is on disk with no")
	cps := fsckRequireRow(t, doc, "checkpoints")
	require.Equal(t, false, cps["ok"], "the orphan itself is still the checkpoints row's defect")
	require.Contains(t, fsckDetail(cps), "orphan")
}

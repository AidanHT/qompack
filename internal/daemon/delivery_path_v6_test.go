package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDeliveryPath_V6_ExtraStagedEvidenceRefusesAdoption(t *testing.T) {
	state := t.TempDir()
	require.NoError(t, createFreshSegment(state, 1, emptyCarry(t, 1)))
	conflict := filepath.Join(segmentDir(state, 1), "interrupted-attempt")
	want := []byte("retain for recovery")
	require.NoError(t, os.WriteFile(conflict, want, 0o600))
	require.Error(t, createFreshSegment(state, 1, emptyCarry(t, 1)))
	got, err := os.ReadFile(conflict)
	require.NoError(t, err)
	require.Equal(t, want, got)
}

func TestDeliveryPath_V6_RotationCloseFailureStopsTheLiveSwitch(t *testing.T) {
	roll := parallelRollover(t, 1)
	root := t.TempDir()
	j := roll.open(t, root)
	_, err := j.lease(context.Background(), genNonce(0), "s", testDeliveryRequest("first"))
	require.NoError(t, err)
	oldPath := j.path
	oldFile := j.file
	j.writer = leaseFaultWriter{file: oldFile, close: func() error {
		_ = oldFile.Close()
		return errors.New("injected close failure")
	}}
	_, err = j.lease(context.Background(), genNonce(1), "s", testDeliveryRequest("second"))
	require.Error(t, err, "uncertain close must not admit a delivery in the new segment")
	require.Equal(t, oldPath, j.path)
	require.False(t, j.usable())
	// The durable transition can be recovered by a new owner, with no second delivery assigned.
	require.NoError(t, j.owner.Release())
	reopened := roll.open(t, root)
	l, err := reopened.lease(context.Background(), genNonce(1), "s", testDeliveryRequest("second"))
	require.NoError(t, err)
	require.Equal(t, uint64(2), l.ArrivalSeq)
}

func TestDeliveryPath_V6_PagePublicationPreservesAConflictingDestination(t *testing.T) {
	r, err := openDeliveryRadix(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { _ = r.close() })
	require.NoError(t, r.root.Mkdir("00", 0o700))
	destination := filepath.Join("00", "existing.page")
	want := []byte("preserve conflicting evidence")
	require.NoError(t, os.WriteFile(filepath.Join(r.dir, destination), want, 0o600))
	err = r.stageAndRename(filepath.Join("00", "stage.tmp"), destination, []byte("replacement"))
	require.Error(t, err, "publication cannot overwrite a page that appeared after the initial check")
	got, err := os.ReadFile(filepath.Join(r.dir, destination))
	require.NoError(t, err)
	require.Equal(t, want, got)
}

func TestDeliveryPath_V6_TransitionLimitRefusesBeforeAppend(t *testing.T) {
	s, _, err := openDeliverySegments(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.close() })
	require.NoError(t, s.initActiveZero())
	before, err := os.ReadFile(s.logPath)
	require.NoError(t, err)
	s.logBytes = deliverySegmentMaxLog
	require.Error(t, s.commitTransition(1, segRoot(1)))
	after, err := os.ReadFile(s.logPath)
	require.NoError(t, err)
	require.Equal(t, before, after, "the writer must stop before exceeding its reader's limit")
}

// The alias is a symlink where the host allows one and an NTFS junction otherwise: an unprivileged
// Windows process cannot create a symlink, but it can create a junction, and pinDeliveryChild
// refuses both (os.ModeSymlink, os.ModeIrregular).
func TestDeliveryPath_V6_RefusesAliasedSegmentParent(t *testing.T) {
	state, outside := t.TempDir(), t.TempDir()
	alias := filepath.Join(state, deliverySegmentsDir)
	if err := makeDirLink(alias, outside); err != nil {
		t.Skip("platform: this host will create neither a directory symlink nor a junction: " + err.Error())
	}
	t.Cleanup(func() { _ = os.Remove(alias) })
	require.Error(t, createFreshSegment(state, 1, emptyCarry(t, 1)))
	entries, err := os.ReadDir(outside)
	require.NoError(t, err)
	require.Empty(t, entries, "no stage may be written through the alias")
}

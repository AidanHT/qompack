package store

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/qompack/qompack/internal/paths"
	"github.com/stretchr/testify/require"
)

func TestBackup_RefusesFrontierChangedBeforeItsOwnCopy(t *testing.T) {
	tp := newTestStore(t)
	m := newMigrator(t, tp, legacySource(2))
	ctx := context.Background()
	_, err := m.Import(ctx)
	require.NoError(t, err)
	head := filepath.Join(paths.Of(tp.Root).State, "delivery-generations", "manifest-head.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(head), 0o700))
	require.NoError(t, os.WriteFile(head, []byte("before\n"), 0o600))
	moved := false
	m.copyBackupFile = func(ctx context.Context, src, dst string) (int64, string, error) {
		if filepath.Clean(src) == filepath.Clean(paths.Long(head)) {
			moved = true
			require.NoError(t, os.WriteFile(head, []byte("after!\n"), 0o600))
		}
		return maintCreateCopy(ctx, tp.Root, src, dst)
	}
	_, err = m.TakeBackup(ctx, "changed-before-own-copy")
	require.True(t, moved, "the test must change a watched frontier during the copy")
	require.ErrorIs(t, err, ErrBackupMoved,
		"equal captured/post-copy bytes cannot prove the frontier stayed still for the whole walk")
}

// These are snapshot mutation tests, not a claim that the synthetic journal
// bytes are accepted by the daemon or an old-release reader.
func TestBackup_RefusesSegmentStateThatMovedUnderTheCopy(t *testing.T) {
	const segment = "state/delivery-segments/00000000000000000001/"
	for _, name := range []string{
		"state/delivery-journal.json",
		"state/delivery-journal-log.jsonl",
		segment + "delivery-leases.jsonl",
		segment + "delivery-acks.jsonl",
		segment + "delivery-lease-position.json",
		segment + "delivery-ack-position.json",
	} {
		t.Run(name, func(t *testing.T) {
			tp := newTestStore(t)
			m := newMigrator(t, tp, legacySource(2))
			ctx := context.Background()
			_, err := m.Import(ctx)
			require.NoError(t, err)
			path := filepath.Join(paths.Of(tp.Root).Dot, filepath.FromSlash(name))
			require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
			require.NoError(t, os.WriteFile(path, []byte("before\n"), 0o600))
			m.afterBackupWalk = func() {
				require.NoError(t, os.WriteFile(path, []byte("after!\n"), 0o600))
			}
			_, err = m.TakeBackup(ctx, "moving-segment")
			require.ErrorIs(t, err, ErrBackupMoved, "equal size is not equal content")
			m.afterBackupWalk = nil
			man, err := m.TakeBackup(ctx, "stable-segment")
			require.NoError(t, err)
			require.True(t, man.Consistent)
			_, err = m.VerifyBackup("stable-segment")
			require.NoError(t, err)
		})
	}
}

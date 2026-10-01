package store

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/paths"
)

// Edge rows for backup, restore, the rollback drill and the migrator's constructor that their main
// tests leave unexecuted: every durability barrier that can fail between a copy and the step that
// certifies or publishes it, the manifest shapes verification refuses, and the drill's own request
// checks (w16b-cover, C3.6). A barrier is failed through the Migrator's paths.Barriers seam.

var errInjectedBarrier = errors.New("injected directory barrier failure")

// failingDirBarrier returns barriers whose directory sync fails for the dirs fail selects, counting
// every call so a test can fail the nth sync of one directory.
func failingDirBarrier(fail func(dir string, call int) bool) paths.Barriers {
	calls := map[string]int{}
	return paths.Barriers{SyncDir: func(dir string) error {
		calls[dir]++
		if fail(dir, calls[dir]) {
			return errInjectedBarrier
		}
		return paths.SyncDir(dir)
	}}
}

// TestTakeBackup_ABarrierFailureLeavesTheBackupUncertified: the manifest certifies a backup tree, so
// it is written only once every name in the tree, and the backup's own entry, is durable. When either
// barrier fails the call fails and no manifest exists, so the backup can never verify.
func TestTakeBackup_ABarrierFailureLeavesTheBackupUncertified(t *testing.T) {
	for name, failOn := range map[string]func(tp *testProject, dir string) bool{
		"tree": func(tp *testProject, dir string) bool {
			return strings.HasPrefix(dir, filepath.Join(paths.Of(tp.Root).Backup, "b1"))
		},
		"backup directory": func(tp *testProject, dir string) bool { return dir == paths.Of(tp.Root).Backup },
	} {
		t.Run(name, func(t *testing.T) {
			tp := newTestStore(t)
			m := newMigrator(t, tp, legacySource(1))
			seedRoot(t, tp, "src/a.ts", "backed up\n")
			m.barriers = failingDirBarrier(func(dir string, _ int) bool { return failOn(tp, dir) })

			_, err := m.TakeBackup(context.Background(), "b1")
			require.ErrorIs(t, err, errInjectedBarrier)
			require.NoFileExists(t, filepath.Join(m.backupDir("b1"), backupManifestFile))
			_, err = m.VerifyBackup("b1")
			require.Error(t, err, "an uncertified backup must not verify")
		})
	}
}

// TestMaintenanceTakeBackup_AnUndurableMarkerOrCertificationIsReported: the pending-certification
// marker must be durable before the engine can write a manifest, and its removal durable before the
// backup is reported certified. backup/ is synced three times in that order — the marker, the
// engine's own entry, the removal — and a failure of the first or the last is reported, not
// swallowed.
func TestMaintenanceTakeBackup_AnUndurableMarkerOrCertificationIsReported(t *testing.T) {
	for _, c := range []struct {
		call   int
		reason string
	}{{1, "marker is not durable"}, {3, "certification is not yet durable"}} {
		t.Run(c.reason, func(t *testing.T) {
			tp := newTestStore(t)
			seedRoot(t, tp, "src/a.ts", "backed up\n")
			x := newMaint(t, tp, leaseOK)
			backupDir := paths.Of(tp.Root).Backup
			x.m.barriers = failingDirBarrier(func(dir string, call int) bool { return dir == backupDir && call == c.call })

			_, err := x.TakeBackup(context.Background(), "m1")
			require.ErrorIs(t, err, errInjectedBarrier)
			require.Contains(t, err.Error(), c.reason)
		})
	}
}

// TestMaintenanceRestore_ABarrierFailureIsReported: a restore is reported done only once its
// destination, its staged tree and the publishing rename are durable. A failed barrier before the
// publish preserves the staging tree and publishes nothing; one after it says the store was published
// but is not yet durable.
func TestMaintenanceRestore_ABarrierFailureIsReported(t *testing.T) {
	cases := []struct {
		name      string
		fail      func(dest, dir string) bool
		reason    string
		published bool
	}{
		{"destination", func(dest, dir string) bool { return dir == filepath.Dir(dest) }, "destination", false},
		{"staged tree", func(dest, dir string) bool { return strings.Contains(dir, ".qompack.restore-") }, "preserved as evidence", false},
		{"publish", func(dest, dir string) bool { return dir == dest }, "could not make it durable", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tp := newTestStore(t)
			seedRoot(t, tp, "src/a.ts", "restored\n")
			x := newMaint(t, tp, leaseOK)
			_, err := x.TakeBackup(context.Background(), "r1")
			require.NoError(t, err)

			dest := filepath.Join(t.TempDir(), "restored")
			x.m.barriers = failingDirBarrier(func(dir string, _ int) bool { return c.fail(dest, dir) })
			_, err = x.Restore(context.Background(), "r1", dest)
			require.ErrorIs(t, err, errInjectedBarrier)
			require.Contains(t, err.Error(), c.reason)
			if c.published {
				require.DirExists(t, filepath.Join(dest, ".qompack"))
			} else {
				require.NoDirExists(t, filepath.Join(dest, ".qompack"), "nothing may be published before its barrier")
			}
		})
	}
}

// TestMaintenance_RefusesAManifestItCannotStandBehind: beside the hostile names the adversarial test
// covers, verification refuses a manifest that does not parse, does not claim a consistent snapshot,
// names one file twice, or names a file emptily, with a NUL, in unclean form or with a control
// character — and an id past its length bound.
func TestMaintenance_RefusesAManifestItCannotStandBehind(t *testing.T) {
	tp := newTestStore(t)
	x := newMaint(t, tp, leaseOK)
	const goodSHA = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	install := func(id string, b []byte) {
		t.Helper()
		dir := x.m.backupDir(id)
		require.NoError(t, os.MkdirAll(paths.Long(filepath.Join(dir, backupTreeDir)), 0o700))
		require.NoError(t, os.WriteFile(paths.Long(filepath.Join(dir, backupManifestFile)), b, 0o600))
	}
	manifest := func(id string, consistent bool, names ...string) []byte {
		man := BackupManifest{Version: backupManifestVersion, ID: id, Consistent: consistent}
		for _, n := range names {
			man.Files = append(man.Files, BackupFile{Name: n, SHA256: goodSHA})
		}
		b, err := json.Marshal(man)
		require.NoError(t, err)
		return b
	}

	cases := map[string][]byte{
		"torn":     []byte(`{"version":`),
		"inconsis": manifest("inconsis", false),
		"twice":    manifest("twice", true, "index/a.json", "index/a.json"),
		"empty":    manifest("empty", true, ""),
		"withnul":  manifest("withnul", true, "index/a\x00b"),
		"unclean":  manifest("unclean", true, "index//a.json"),
		"control":  manifest("control", true, "index/a\x01b.json"),
	}
	for id, b := range cases {
		install(id, b)
		_, err := x.VerifyBackup(id)
		require.ErrorIsf(t, err, ErrBackupManifest, "verify must refuse manifest %q", id)
	}
	_, err := x.VerifyBackup(strings.Repeat("a", 129))
	require.ErrorIs(t, err, ErrBackupID)
}

// TestMaintenanceRestore_RefusesADestinationDotThatIsAFile: a destination whose .qompack is a regular
// file is occupied, and the refusal names what is in the way.
func TestMaintenanceRestore_RefusesADestinationDotThatIsAFile(t *testing.T) {
	tp := newTestStore(t)
	seedRoot(t, tp, "src/a.ts", "a\n")
	x := newMaint(t, tp, leaseOK)
	_, err := x.TakeBackup(context.Background(), "f1")
	require.NoError(t, err)

	dest := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dest, ".qompack"), []byte("not a store"), 0o600))
	_, err = x.Restore(context.Background(), "f1", dest)
	require.ErrorIs(t, err, ErrRestoreTargetExists)
	require.Contains(t, err.Error(), "is a file")
}

// TestRehearseRollback_RefusesAnIncompleteRequest: the drill needs a known phase and a scratch root,
// or it is an error before anything runs; without a way to stop the writers, or in the after phase
// with no new-format write to rehearse against, it is a recorded refusal.
func TestRehearseRollback_RefusesAnIncompleteRequest(t *testing.T) {
	tp := newTestStore(t)
	m := newMigrator(t, tp, legacySource(1))
	ctx := context.Background()
	_, err := m.Import(ctx)
	require.NoError(t, err)
	_, err = m.TakeBackup(ctx, "d1")
	require.NoError(t, err)
	stop := func(context.Context) error { return nil }
	scratch := func() string { return filepath.Join(t.TempDir(), "scratch") }

	_, err = m.RehearseRollback(ctx, RollbackOptions{Phase: "sideways", BackupID: "d1", RestoreRoot: scratch(), StopWriters: stop})
	require.ErrorContains(t, err, "unknown phase")
	_, err = m.RehearseRollback(ctx, RollbackOptions{Phase: RollbackBeforeFirstNewWrite, BackupID: "d1", StopWriters: stop})
	require.ErrorContains(t, err, "scratch restore root")

	drill, err := m.RehearseRollback(ctx, RollbackOptions{Phase: RollbackBeforeFirstNewWrite, BackupID: "d1", RestoreRoot: scratch()})
	require.NoError(t, err)
	require.False(t, drill.OK)
	require.Contains(t, drill.Refusal, "no way to stop")

	drill, err = m.RehearseRollback(ctx, RollbackOptions{Phase: RollbackAfterFirstNewWrite, BackupID: "d1", RestoreRoot: scratch(), StopWriters: stop})
	require.NoError(t, err)
	require.False(t, drill.OK)
	require.Contains(t, drill.Refusal, "real new artifact")
}

// TestNewMigrator_RefusesAMissingStoreOrSource: an open gate is not enough — the migrator needs the
// destination store and the legacy source it moves between.
func TestNewMigrator_RefusesAMissingStoreOrSource(t *testing.T) {
	tp := newTestStore(t)
	_, err := NewMigrator(nil, tp.Root, MigrateOptions{Source: legacySource(1), Gate: passedGate()})
	require.ErrorContains(t, err, "destination store")
	_, err = NewMigrator(tp.Store, tp.Root, MigrateOptions{Gate: passedGate()})
	require.ErrorContains(t, err, "legacy source")
}

// TestMigratorHandoff_ReadsAnOwnerlessRecordAsLegacyAndRefusesADamagedOne: a handoff record that names
// no owner is the legacy writer's, as an absent one is; one that does not parse, or cannot be read, is
// an error rather than either owner.
func TestMigratorHandoff_ReadsAnOwnerlessRecordAsLegacyAndRefusesADamagedOne(t *testing.T) {
	tp := newTestStore(t)
	m := newMigrator(t, tp, legacySource(1))
	p := m.path(handoffFile)

	require.NoError(t, os.WriteFile(paths.Long(p), []byte(`{"version":1}`), 0o600))
	h, err := m.Handoff()
	require.NoError(t, err)
	require.Equal(t, WriterLegacy, h.Owner)

	require.NoError(t, os.WriteFile(paths.Long(p), []byte(`{"version":`), 0o600))
	_, err = m.Handoff()
	require.ErrorContains(t, err, "parse handoff")

	require.NoError(t, os.Remove(paths.Long(p)))
	require.NoError(t, os.Mkdir(paths.Long(p), 0o700))
	_, err = m.Handoff()
	require.ErrorContains(t, err, "read handoff")
	_, err = m.TakeBackup(context.Background(), "h1")
	require.ErrorContains(t, err, "read handoff", "a backup needs to know who the writer is")
}

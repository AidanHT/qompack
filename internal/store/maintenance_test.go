package store

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/paths"
)

// The maintenance seam is the supported operator backup/verify/restore capability (V6-RECOVERY-2).
// These tests exercise the hostile and durable-data properties the hardening round named: unsafe ids
// and manifest paths (traversal, absolute, drive/ADS colon, reserved and trailing-dot components,
// case-fold collisions, bad digest/size, id mismatch, unknown schema), the fail-closed writer-lease
// guard checked before AND after the copy, corruption, a destination conflict refused before any
// write, post-copy corruption caught before publication, symlink components refused in the backup
// tree, context cancellation, a non-vacuous same-build readback, and a point-in-time restore that
// leaves the source's later writes intact.

func leaseOK() error { return nil }

func newMaint(t *testing.T, tp *testProject, lease func() error) *Maintenance {
	t.Helper()
	x, err := NewMaintenance(tp.Store, tp.Root, MaintenanceOptions{
		Cfg: tp.Cfg, Clock: tp.Clock, WriterLeaseHeld: lease,
	})
	require.NoError(t, err)
	return x
}

func openPlain(t *testing.T, root string, cfg config.Config, clk core.Clock) Store {
	t.Helper()
	s, err := Open(root, cfg, Deps{Clock: clk, Log: logging.Nop()})
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestMaintenance_ConstructorRequiresExplicitConfig(t *testing.T) {
	tp := newTestStore(t)
	_, err := NewMaintenance(tp.Store, tp.Root, MaintenanceOptions{Clock: tp.Clock})
	require.ErrorIs(t, err, ErrMaintenanceConfig,
		"a zero config must be refused, never silently replaced with Defaults()")

	x, err := NewMaintenance(tp.Store, tp.Root, MaintenanceOptions{Cfg: tp.Cfg, Clock: tp.Clock})
	require.NoError(t, err)
	require.NotNil(t, x)
}

func TestMaintenance_TakeBackupLeaseGuard(t *testing.T) {
	tp := newTestStore(t)
	seedRoot(t, tp, "src/a.ts", "content a\n")
	ctx := context.Background()

	// No guard at all: fail closed.
	_, err := newMaint(t, tp, nil).TakeBackup(ctx, "b1")
	require.ErrorIs(t, err, ErrWriterLeaseRequired)

	// The guard reports the lease is not held before the copy: fail closed.
	_, err = newMaint(t, tp, func() error { return errAssertLeaseDown }).TakeBackup(ctx, "b1")
	require.ErrorIs(t, err, ErrWriterLeaseRequired)

	// The lease is lost DURING the copy (held on the first check, gone on the second): the artifact is
	// preserved but the call fails rather than claiming success.
	var calls int
	lost := newMaint(t, tp, func() error {
		calls++
		if calls >= 2 {
			return errAssertLeaseDown
		}
		return nil
	})
	_, err = lost.TakeBackup(ctx, "b2")
	require.ErrorIs(t, err, ErrWriterLeaseRequired)
	require.Contains(t, err.Error(), "preserved")
	require.DirExists(t, paths.Long(lost.m.backupDir("b2")), "the artifact must be preserved for inspection")
	_, err = lost.VerifyBackup("b2")
	require.ErrorIs(t, err, ErrBackupManifest, "a failed writer certification must survive into later verification")
	_, err = lost.Restore(ctx, "b2", filepath.Join(t.TempDir(), "uncertified"))
	require.ErrorIs(t, err, ErrBackupManifest)

	// A confirmed lease throughout: the backup is taken.
	man, err := newMaint(t, tp, leaseOK).TakeBackup(ctx, "b3")
	require.NoError(t, err)
	require.NotEmpty(t, man.Files)
}

var errAssertLeaseDown = &leaseDownError{}

type leaseDownError struct{}

func (*leaseDownError) Error() string { return "the daemon still holds the writer lock" }

func TestMaintenance_BackupIDValidation(t *testing.T) {
	tp := newTestStore(t)
	x := newMaint(t, tp, leaseOK)
	ctx := context.Background()

	for _, id := range []string{
		"", ".", "..", "../escape", "a/b", `a\b`, "a b", "a:b",
		".hidden", "con", "COM1.bak", "NUL", "lpt9", "a\tb",
	} {
		_, err := x.TakeBackup(ctx, id)
		require.ErrorIsf(t, err, ErrBackupID, "id %q must be rejected", id)
		_, verr := x.VerifyBackup(id)
		require.ErrorIsf(t, verr, ErrBackupID, "id %q must be rejected by verify too", id)
	}

	seedRoot(t, tp, "src/ok.ts", "ok\n")
	man, err := x.TakeBackup(ctx, "sp17-pre_cutover.1")
	require.NoError(t, err)
	require.NotEmpty(t, man.Files)
	_, err = x.VerifyBackup("sp17-pre_cutover.1")
	require.NoError(t, err)
}

func TestMaintenance_TakeVerifyRestoreRoundTrip(t *testing.T) {
	tp := newTestStore(t)
	ctx := context.Background()
	a := seedRoot(t, tp, "src/a.ts", "payload a — must survive a backup and restore\n")
	b := seedRoot(t, tp, "src/b.ts", "payload b — the second root\n")

	x := newMaint(t, tp, leaseOK)
	_, err := x.TakeBackup(ctx, "rt")
	require.NoError(t, err)
	_, err = x.VerifyBackup("rt")
	require.NoError(t, err)

	dest := filepath.Join(t.TempDir(), "restored")
	proof, err := x.Restore(ctx, "rt", dest)
	require.NoError(t, err, "refusal note: %s", proof.Note)
	require.True(t, proof.OpenedOK)
	require.True(t, proof.SameBuildOnly)
	require.False(t, proof.CheckpointSealCovered, "the seam never claims checkpoint/seal coverage")
	require.GreaterOrEqual(t, proof.ContentRootsProven, 2, "both seeded roots must read back")

	require.DirExists(t, paths.Long(filepath.Join(dest, ".qompack")))
	rs := openPlain(t, dest, tp.Cfg, tp.Clock)
	for _, h := range []core.Hash{a, b} {
		got, gerr := readRoot(ctx, rs, h)
		require.NoError(t, gerr, "restored store must serve root %s", h.Short())
		require.NotEmpty(t, got)
	}
}

func TestMaintenance_RestoreRefusesExistingDestination(t *testing.T) {
	tp := newTestStore(t)
	ctx := context.Background()
	seedRoot(t, tp, "src/a.ts", "a\n")
	x := newMaint(t, tp, leaseOK)
	_, err := x.TakeBackup(ctx, "rc")
	require.NoError(t, err)

	dest := filepath.Join(t.TempDir(), "occupied")
	require.NoError(t, os.MkdirAll(paths.Long(filepath.Join(dest, ".qompack")), 0o700))
	sentinel := filepath.Join(dest, ".qompack", "sentinel")
	require.NoError(t, os.WriteFile(paths.Long(sentinel), []byte("do not touch"), 0o600))

	_, err = x.Restore(ctx, "rc", dest)
	require.ErrorIs(t, err, ErrRestoreTargetExists)

	got, rerr := os.ReadFile(paths.Long(sentinel))
	require.NoError(t, rerr)
	require.Equal(t, "do not touch", string(got))
	entries, derr := os.ReadDir(paths.Long(dest))
	require.NoError(t, derr)
	for _, e := range entries {
		require.NotContains(t, e.Name(), ".qompack.restore-",
			"a refused restore must not leave a staging directory")
	}
}

func TestMaintenance_CorruptedBackupIsRefused(t *testing.T) {
	tp := newTestStore(t)
	ctx := context.Background()
	seedRoot(t, tp, "src/a.ts", "a payload that will be corrupted in its backup copy\n")
	x := newMaint(t, tp, leaseOK)
	man, err := x.TakeBackup(ctx, "cor")
	require.NoError(t, err)
	require.NotEmpty(t, man.Files)

	tree := filepath.Join(x.m.backupDir("cor"), backupTreeDir)
	target := filepath.Join(tree, filepath.FromSlash(man.Files[0].Name))
	require.NoError(t, os.WriteFile(paths.Long(target), []byte("corrupted bytes, different length"), 0o600))

	_, err = x.VerifyBackup("cor")
	require.ErrorIs(t, err, ErrBackupCorrupt)

	dest := filepath.Join(t.TempDir(), "restored")
	_, err = x.Restore(ctx, "cor", dest)
	require.ErrorIs(t, err, ErrBackupCorrupt)
	require.NoDirExists(t, paths.Long(filepath.Join(dest, ".qompack")),
		"a corrupted backup must never publish a destination store")
}

func TestMaintenance_AdversarialManifestRefusedBeforeReadingFiles(t *testing.T) {
	tp := newTestStore(t)
	ctx := context.Background()
	x := newMaint(t, tp, leaseOK)
	const goodSHA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

	write := func(id string, man BackupManifest) {
		t.Helper()
		man.Consistent = true // exercise the named hostile input, not the consistency precondition
		dir := x.m.backupDir(id)
		require.NoError(t, os.MkdirAll(paths.Long(filepath.Join(dir, backupTreeDir)), 0o700))
		b, err := json.Marshal(man)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(paths.Long(filepath.Join(dir, backupManifestFile)), b, 0o600))
	}
	file := func(name string) []BackupFile { return []BackupFile{{Name: name, Size: 0, SHA256: goodSHA}} }

	cases := map[string]BackupManifest{
		"esc":   {Version: backupManifestVersion, ID: "esc", Files: file("../evil")},
		"abs":   {Version: backupManifestVersion, ID: "abs", Files: file("/etc/passwd")},
		"bslsh": {Version: backupManifestVersion, ID: "bslsh", Files: file(`objects\evil`)},
		"ads":   {Version: backupManifestVersion, ID: "ads", Files: file("objects/ab:cd")},
		"resvd": {Version: backupManifestVersion, ID: "resvd", Files: file("objects/con/data")},
		"trail": {Version: backupManifestVersion, ID: "trail", Files: file("objects/ab./x")},
		"sha":   {Version: backupManifestVersion, ID: "sha", Files: []BackupFile{{Name: "index/x.json", Size: 0, SHA256: "zz"}}},
		"size":  {Version: backupManifestVersion, ID: "size", Files: []BackupFile{{Name: "index/y.json", Size: -1, SHA256: goodSHA}}},
		"idmis": {Version: backupManifestVersion, ID: "someone-else", Files: nil},
		"schem": {Version: backupManifestVersion + 98, ID: "schem", Files: nil},
		"fold": {Version: backupManifestVersion, ID: "fold", Files: []BackupFile{
			{Name: "index/Roots.jsonl", Size: 0, SHA256: goodSHA},
			{Name: "index/roots.jsonl", Size: 0, SHA256: goodSHA},
		}},
	}
	for id, man := range cases {
		write(id, man)
		_, verr := x.VerifyBackup(id)
		require.ErrorIsf(t, verr, ErrBackupManifest, "verify must refuse manifest %q", id)
		_, rerr := x.Restore(ctx, id, filepath.Join(t.TempDir(), id))
		require.ErrorIsf(t, rerr, ErrBackupManifest, "restore must refuse manifest %q", id)
	}
}

func TestMaintenance_SymlinkComponentInBackupTreeRefused(t *testing.T) {
	tp := newTestStore(t)
	ctx := context.Background()
	seedRoot(t, tp, "src/a.ts", "a\n")
	x := newMaint(t, tp, leaseOK)
	man, err := x.TakeBackup(ctx, "sym")
	require.NoError(t, err)

	// Replace one backup-tree file with a symlink pointing outside the tree.
	victim := filepath.Join(x.m.backupDir("sym"), backupTreeDir, filepath.FromSlash(man.Files[0].Name))
	external := filepath.Join(t.TempDir(), "external-secret")
	require.NoError(t, os.WriteFile(paths.Long(external), []byte("outside the backup"), 0o600))
	require.NoError(t, os.Remove(paths.Long(victim)))
	// A file leaf has no unprivileged Windows equivalent (a junction aliases directories only), so
	// on such a host this case is skipped; TestMaintenance_DirectoryLinkComponentInBackupTreeRefused
	// covers the directory-component case there.
	if err := os.Symlink(external, victim); err != nil {
		t.Skip("platform: this host will not create a file symlink: " + err.Error())
	}

	_, err = x.VerifyBackup("sym")
	require.ErrorIs(t, err, ErrBackupManifest, "a symlink component must be refused, not followed")
	dest := filepath.Join(t.TempDir(), "restored")
	_, err = x.Restore(ctx, "sym", dest)
	require.ErrorIs(t, err, ErrBackupManifest)
	require.NoDirExists(t, paths.Long(filepath.Join(dest, ".qompack")))
}

// A directory component of the backup tree replaced by an alias to a byte-identical copy outside the
// tree. Every hash would still match if the alias were followed, so ErrBackupManifest (not success,
// and not ErrBackupCorrupt) can only come from the no-follow check. The alias is a symlink where the
// host allows one and an NTFS junction otherwise, which maintNoFollow refuses as os.ModeIrregular.
func TestMaintenance_DirectoryLinkComponentInBackupTreeRefused(t *testing.T) {
	tp := newTestStore(t)
	ctx := context.Background()
	seedRoot(t, tp, "src/a.ts", "a\n")
	x := newMaint(t, tp, leaseOK)
	man, err := x.TakeBackup(ctx, "dirlink")
	require.NoError(t, err)

	var component string
	for _, f := range man.Files {
		if first, _, nested := strings.Cut(f.Name, "/"); nested {
			component = first
			break
		}
	}
	require.NotEmpty(t, component, "the backup must hold a file below a directory for this fixture")

	aliased := filepath.Join(x.m.backupDir("dirlink"), backupTreeDir, component)
	external := filepath.Join(t.TempDir(), "external-"+component)
	require.NoError(t, os.Rename(paths.Long(aliased), paths.Long(external)))
	if err := makeDirLink(aliased, external); err != nil {
		t.Skip("platform: this host will create neither a directory symlink nor a junction: " + err.Error())
	}
	t.Cleanup(func() { _ = os.Remove(paths.Long(aliased)) })

	_, err = x.VerifyBackup("dirlink")
	require.ErrorIs(t, err, ErrBackupManifest, "a linked directory component must be refused, not followed")
	dest := filepath.Join(t.TempDir(), "restored")
	_, err = x.Restore(ctx, "dirlink", dest)
	require.ErrorIs(t, err, ErrBackupManifest)
	require.NoDirExists(t, paths.Long(filepath.Join(dest, ".qompack")))
}

func TestMaintenance_ContextCancellationStops(t *testing.T) {
	tp := newTestStore(t)
	seedRoot(t, tp, "src/a.ts", "a\n")
	x := newMaint(t, tp, leaseOK)
	_, err := x.TakeBackup(context.Background(), "ctx")
	require.NoError(t, err)

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()

	// verifyTree is the cancellable core VerifyBackup runs under a background context.
	man, err := x.readValidatedManifest("ctx")
	require.NoError(t, err)
	require.ErrorIs(t, x.verifyTree(cancelled, "ctx", man), context.Canceled)

	// Restore threads the caller's context through staging, hashing and copying.
	_, err = x.Restore(cancelled, "ctx", filepath.Join(t.TempDir(), "restored"))
	require.ErrorIs(t, err, context.Canceled)
}

func TestMaintenance_StagedRehashCatchesPostCopyCorruption(t *testing.T) {
	tp := newTestStore(t)
	ctx := context.Background()
	seedRoot(t, tp, "src/a.ts", "a\n")
	x := newMaint(t, tp, leaseOK)
	_, err := x.TakeBackup(ctx, "pc")
	require.NoError(t, err)

	man, err := x.readValidatedManifest("pc")
	require.NoError(t, err)

	staging := filepath.Join(t.TempDir(), "staging")
	require.NoError(t, x.stageRestore(ctx, "pc", man, staging))
	stagedDot := paths.Of(staging).Dot
	require.NoError(t, x.verifyStaged(ctx, stagedDot, man))

	// Corrupt a NON-protected staged file (objects/index), which the pre-publication rehash re-reads.
	var victim string
	for _, f := range man.Files {
		if !paths.IsProtected(staging, filepath.Join(stagedDot, filepath.FromSlash(f.Name))) {
			victim = filepath.Join(stagedDot, filepath.FromSlash(f.Name))
			break
		}
	}
	require.NotEmpty(t, victim, "a backup must contain at least one non-protected file")
	require.NoError(t, os.WriteFile(paths.Long(victim), []byte("tampered after staging, before publish"), 0o600))
	require.ErrorIs(t, x.verifyStaged(ctx, stagedDot, man), ErrBackupCorrupt)
}

func TestMaintenance_RestoreLeavesSourceLaterWritesIntact(t *testing.T) {
	tp := newTestStore(t)
	ctx := context.Background()
	before := seedRoot(t, tp, "src/before.ts", "recorded before the backup\n")

	x := newMaint(t, tp, leaseOK)
	_, err := x.TakeBackup(ctx, "pit")
	require.NoError(t, err)

	after := seedRoot(t, tp, "src/after.ts", "recorded after the backup — a later write\n")

	dest := filepath.Join(t.TempDir(), "restored")
	proof, err := x.Restore(ctx, "pit", dest)
	require.NoError(t, err, "refusal note: %s", proof.Note)

	_, err = tp.Store.GetRoot(ctx, before)
	require.NoError(t, err, "the backup must not have disturbed the source")
	_, err = tp.Store.GetRoot(ctx, after)
	require.NoError(t, err, "the later write must remain in the source")

	rs := openPlain(t, dest, tp.Cfg, tp.Clock)
	_, err = rs.GetRoot(ctx, before)
	require.NoError(t, err, "the restored store must hold the pre-backup root")
	_, err = rs.GetRoot(ctx, after)
	require.Error(t, err, "a point-in-time restore must not contain a write made after the backup")
}

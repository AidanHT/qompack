package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/daemon"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/store"
	"github.com/stretchr/testify/require"
)

func TestBackupCLI_ConsistentRestoreRetainsLaterSourceWrites(t *testing.T) {
	p := seedFsckProject(t)
	code, output, stderr := fsckDispatch(t, "backup", "create", "--project", p.Root, "--id", "before", "--json")
	require.Equal(t, ExitOK, code, "%s %s", output, stderr)
	var created backupReport
	require.NoError(t, json.Unmarshal([]byte(output), &created))
	require.NotNil(t, created.Manifest)
	require.True(t, created.Manifest.Consistent)
	require.NotEmpty(t, created.Manifest.Files)
	code, output, stderr = fsckDispatch(t, "backup", "verify", "--project", p.Root, "--id", "before", "--json")
	require.Equal(t, ExitOK, code, "%s %s", output, stderr)
	s, err := store.Open(p.Root, config.Defaults(), store.Deps{})
	require.NoError(t, err)
	later, err := s.PutBytes(context.Background(), []byte("later source write survives restore"), store.PutOptions{Tool: "Bash"})
	require.NoError(t, err)
	require.NoError(t, s.Close())
	beforeRestore := snapshotQompack(t, p.Layot)
	dest := filepath.Join(t.TempDir(), "restored project")
	code, output, stderr = fsckDispatch(t, "backup", "restore", "--project", p.Root, "--id", "before", "--destination", dest, "--json")
	require.Equal(t, ExitOK, code, "%s %s", output, stderr)
	var restored backupReport
	require.NoError(t, json.Unmarshal([]byte(output), &restored))
	require.NotNil(t, restored.Restore)
	require.Positive(t, restored.Restore.ContentRootsProven)
	require.True(t, restored.Restore.SameBuildOnly)
	require.NotNil(t, restored.Integrity)
	require.Equal(t, ExitOK, restored.Integrity.Exit)
	require.Equal(t, beforeRestore, snapshotQompack(t, p.Layot), "restore must not rewrite the source store")
	reader, err := store.OpenReadOnly(p.Root, config.Defaults(), store.Deps{})
	require.NoError(t, err)
	_, err = reader.GetRoot(context.Background(), later.Root.Hash)
	require.NoError(t, err, "restore must retain source writes made after the backup")
	require.NoError(t, reader.Close())
	code, output, _ = fsckDispatch(t, "backup", "restore", "--project", p.Root, "--id", "before", "--destination", dest, "--json")
	require.Equal(t, ExitError, code)
	require.Contains(t, output, "already exists")
}

func TestBackupCLI_RefusesLiveWriterAndDoesNotOpenStore(t *testing.T) {
	p := seedFsckProject(t)
	addr, err := ipc.Resolve(p.Root)
	require.NoError(t, err)
	lock, err := daemon.AcquireLock(p.Root, addr, core.SystemClock())
	require.NoError(t, err)
	defer func() { require.NoError(t, lock.Release()) }()
	code, output, _ := fsckDispatch(t, "backup", "create", "--project", p.Root, "--id", "blocked", "--json")
	require.Equal(t, ExitError, code)
	require.Contains(t, output, "stop the source daemon")
	_, err = os.Stat(filepath.Join(p.Root, ".qompack", "backup", "blocked"))
	require.True(t, os.IsNotExist(err))
	// A competing packaged bootstrap must also stop before it opens the store.
	code, _, _ = fsckDispatch(t, "daemon", "--project", p.Root, "--foreground")
	require.Equal(t, ExitOK, code)
	require.NoError(t, lock.Heartbeat(), "a failed bootstrap must not release the existing writer's lease")
}

func TestBackupCLI_UncertifiedSnapshotIsRefusedByEveryReader(t *testing.T) {
	p := seedFsckProject(t)
	code, out, stderr := fsckDispatch(t, "backup", "create", "--project", p.Root, "--id", "uncertified", "--json")
	require.Equal(t, ExitOK, code, "%s %s", out, stderr)
	marker := filepath.Join(p.Root, ".qompack", "backup", ".uncertified.certification-pending")
	require.NoError(t, os.WriteFile(marker, []byte("writer lease was not confirmed\n"), 0o600))
	code, out, _ = fsckDispatch(t, "backup", "verify", "--project", p.Root, "--id", "uncertified", "--json")
	require.Equal(t, ExitError, code)
	require.Contains(t, out, "certification")
	dest := filepath.Join(t.TempDir(), "restore")
	code, out, _ = fsckDispatch(t, "backup", "restore", "--project", p.Root, "--id", "uncertified", "--destination", dest, "--json")
	require.Equal(t, ExitError, code)
	require.Contains(t, out, "certification")
	require.NoDirExists(t, filepath.Join(dest, ".qompack"))
	code, out, _ = fsckDispatch(t, "fsck", "--project", p.Root, "--json")
	require.Equal(t, ExitError, code)
	require.Contains(t, out, "certification")
	require.FileExists(t, marker)
}

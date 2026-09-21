package fault

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
)

// Packaged regression for the V6 operator backup/verify/restore capability (V6-RECOVERY-2). It drives
// the REAL bundled binary's `backup create|verify|restore` commands — which acquire the actual daemon
// writer lease before opening the store — over projects seeded by the real hooks. The claims are
// same-build only: no older-reader compatibility is asserted anywhere.
//
// Three bounded cases: a create/verify/restore round-trip that preserves the source, its later writes
// and the original backup while the restore is point-in-time; the two refusals that must leave no
// damage (a live writer, an occupied destination); and a corrupted / unknown-schema backup that must
// fail without publishing a destination store.

// backupResult mirrors the fields of `qompack backup … --json` (internal/cli/backup.go backupReport).
// store.RestoreProof carries no JSON tags, so its keys are the Go field names; encoding/json decodes
// them into the equally named fields below.
type backupResult struct {
	Schema    int                 `json:"schema"`
	Action    string              `json:"action"`
	ID        string              `json:"id"`
	Manifest  *backupManifestJSON `json:"manifest"`
	Restore   *restoreProofJSON   `json:"restore"`
	Integrity *integrityJSON      `json:"integrity"`
	Error     string              `json:"error"`
}

type backupManifestJSON struct {
	Version int    `json:"version"`
	ID      string `json:"id"`
	Files   []struct {
		Name   string `json:"name"`
		Size   int64  `json:"size"`
		SHA256 string `json:"sha256"`
	} `json:"files"`
}

type restoreProofJSON struct {
	BackupID              string
	Destination           string
	OpenedOK              bool
	ContentRootsProven    int
	ToolRefsProven        int
	SameBuildOnly         bool
	CheckpointSealCovered bool
	Note                  string
}

type integrityJSON struct {
	Schema int `json:"schema"`
	Exit   int `json:"exit"`
	Checks []struct {
		ID string `json:"id"`
		OK bool   `json:"ok"`
	} `json:"checks"`
}

type backupInvocation struct {
	res    backupResult
	code   int
	stdout string
	stderr string
}

// runBackupJSON runs one packaged `backup` subcommand with --json and decodes the structured result.
// A non-zero exit is returned, never asserted: several cases exist to observe a refusal.
func runBackupJSON(t *testing.T, b bundle, cwd string, args []string, env map[string]string) backupInvocation {
	t.Helper()
	stdout, stderr, code := run(t, b.Bin, cwd, args, nil, env)
	var res backupResult
	require.NoError(t, json.Unmarshal(stdout, &res),
		"backup --json must emit a structured result\nargs=%v\nstdout=%s\nstderr=%s", args, stdout, stderr)
	return backupInvocation{res: res, code: code, stdout: string(stdout), stderr: string(stderr)}
}

// requireNoPublishedStore asserts that no .qompack was published at root — the destination guarantee a
// refused or failed restore must keep.
func requireNoPublishedStore(t *testing.T, root string) {
	t.Helper()
	_, err := os.Stat(paths.Long(paths.Of(root).Dot))
	require.Truef(t, os.IsNotExist(err), "no destination .qompack must have been published at %s (err=%v)", root, err)
}

// TestV6Backup_RoundTripPreservesSourceLaterWritesAndBackup drives create → verify → a real later
// write → restore into a fresh destination, and proves the restore is a verified point-in-time copy
// that leaves the source, its later writes and the original backup untouched.
func TestV6Backup_RoundTripPreservesSourceLaterWritesAndBackup(t *testing.T) {
	b := assembledBundle(t)
	p := newProject(t, "source")
	t.Cleanup(func() {
		shutdownIfReachable(t, p.Root)
		requireNoOrphan(t, p.Root)
	})

	rec := newRecord(t, "v6_backup_roundtrip_preserves_source")
	rec.Phase = PhaseLifecycle
	rec.Boundary = "operator backup/verify/restore of a stopped store into a fresh destination"
	rec.SeedMethod = "packaged hooks seed a store and captures; daemon stopped; backup create/verify; a real hook then writes a new root; daemon stopped; restore into a fresh destination"
	rec.Outcome = OutcomeFailed
	rec.Reason = "the packaged backup round-trip did not complete or did not preserve the source, later writes, or the original backup"
	defer func() { writeRecord(t, rec) }()

	const seed = core.SessionID("sess-fault-v6-backup-seed")
	seedSession(t, b, p, seed)
	shutdownIfReachable(t, p.Root)

	create := runBackupJSON(t, b, p.Root,
		[]string{"backup", "create", "--project", p.Root, "--id", "before", "--json"}, p.Env)
	require.Equal(t, 0, create.code, "backup create must succeed with the daemon stopped: %s", create.stderr)
	require.NotNil(t, create.res.Manifest, "create must return a manifest")
	require.NotEmpty(t, create.res.Manifest.Files, "a backup of a recorded project is not empty")

	verify := runBackupJSON(t, b, p.Root,
		[]string{"backup", "verify", "--project", p.Root, "--id", "before", "--json"}, p.Env)
	require.Equal(t, 0, verify.code, "backup verify must succeed: %s", verify.stderr)
	require.NotNil(t, verify.res.Manifest)

	// Point-in-time snapshot of the source objects and the original backup manifest, before any later
	// write: nothing writes between create and here, so this is exactly what the backup captured.
	preLater := objectFingerprints(t, p.Root)
	require.NotEmpty(t, preLater)
	backupManifestPath := filepath.Join(paths.Of(p.Root).Backup, "before", "manifest.json")
	backupManifestBefore := readAll(t, backupManifestPath)

	// A genuine later write (new content) through the real hook, then stop.
	const later = core.SessionID("sess-fault-v6-backup-later")
	recoverSession(t, b, p, later)
	shutdownIfReachable(t, p.Root)
	sourceAfterLater := objectFingerprints(t, p.Root)
	require.Empty(t, changedObjects(sourceAfterLater, preLater), "a later write must not disturb earlier objects")
	require.Greater(t, len(sourceAfterLater), len(preLater), "the later hook must have added a new root to the source")

	// Restore into a fresh, isolated destination.
	dest := newProject(t, "restored").Root
	restore := runBackupJSON(t, b, p.Root,
		[]string{"backup", "restore", "--project", p.Root, "--id", "before", "--destination", dest, "--json"}, p.Env)
	require.Equal(t, 0, restore.code, "backup restore must succeed into a fresh destination: %s", restore.stderr)
	require.NotNil(t, restore.res.Restore, "restore must report a proof")
	require.True(t, restore.res.Restore.OpenedOK)
	require.True(t, restore.res.Restore.SameBuildOnly, "the proof is a same-build read; no older-reader claim is made")
	require.False(t, restore.res.Restore.CheckpointSealCovered, "the content reader must not claim checkpoint/seal coverage")
	require.GreaterOrEqual(t, restore.res.Restore.ContentRootsProven, 1,
		"the same-build reader must read at least one content root back (non-vacuous)")
	require.NotNil(t, restore.res.Integrity, "restore must attach the packaged fsck --seal-check integrity report")
	require.Equal(t, 0, restore.res.Integrity.Exit, "the restored destination must pass its integrity check")

	// Point-in-time: the destination holds exactly the backed-up objects, not the later write.
	require.Equal(t, preLater, objectFingerprints(t, dest),
		"the restore is point-in-time: the destination must hold the backed-up objects and not the later write")

	// The source, its later write, and the original backup are all unchanged by the restore.
	require.Empty(t, changedObjects(objectFingerprints(t, p.Root), sourceAfterLater), "restore must not disturb the source")
	require.Equal(t, backupManifestBefore, readAll(t, backupManifestPath), "the original backup must be unchanged by a restore")

	rec.Outcome = OutcomeRecovered
	rec.Reason = "packaged backup create/verify with the daemon stopped, then restore into a fresh destination: same-build content read proof and fsck --seal-check integrity pass; the point-in-time destination excludes the later write; the source, its later write and the original backup are unchanged. Same-build only; no older-reader compatibility is claimed."
	rec.Detail = fmt.Sprintf("roots_proven=%d; integrity_exit=%d; source_objects=%d; dest_objects=%d",
		restore.res.Restore.ContentRootsProven, restore.res.Integrity.Exit, len(sourceAfterLater), len(preLater))
}

// TestV6Backup_RefusesLiveWriterAndTargetConflict proves the two refusals that must leave no damage:
// a create attempted beside a live daemon, and a restore aimed at an occupied destination.
func TestV6Backup_RefusesLiveWriterAndTargetConflict(t *testing.T) {
	b := assembledBundle(t)
	p := newProject(t, "source")
	t.Cleanup(func() {
		shutdownIfReachable(t, p.Root)
		requireNoOrphan(t, p.Root)
	})

	rec := newRecord(t, "v6_backup_refuses_live_writer_and_target_conflict")
	rec.Phase = PhaseLifecycle
	rec.Boundary = "backup create beside a live daemon; restore into an occupied destination"
	rec.SeedMethod = "packaged hooks seed a store and leave the daemon up; backup create is attempted while it holds the lease; then the daemon is stopped, a backup is taken, and restore is aimed at a destination that already holds a .qompack"
	rec.Outcome = OutcomeFailed
	rec.Reason = "a refusal did not occur, or a refusal damaged the source or the destination"
	defer func() { writeRecord(t, rec) }()

	const seed = core.SessionID("sess-fault-v6-backup-refuse")
	seedSession(t, b, p, seed) // leaves the daemon UP
	require.True(t, waitDaemonUp(t, p.Root), "the daemon must be up for the live-writer refusal to be meaningful")

	// (1) Live writer: the CLI acquires the daemon lease before opening the store, so a running daemon
	//     makes create refuse and create no artifact.
	before := objectFingerprints(t, p.Root)
	live := runBackupJSON(t, b, p.Root,
		[]string{"backup", "create", "--project", p.Root, "--id", "live", "--json"}, p.Env)
	require.NotEqual(t, 0, live.code, "backup create must refuse while the daemon holds the writer lease")
	require.Contains(t, live.res.Error, "daemon",
		"the refusal must tell the operator to stop the source daemon: %q", live.res.Error)
	_, statErr := os.Stat(paths.Long(filepath.Join(paths.Of(p.Root).Backup, "live")))
	require.True(t, os.IsNotExist(statErr), "a refused backup must create no artifact")
	require.Empty(t, changedObjects(objectFingerprints(t, p.Root), before), "a refused backup must not touch the store")

	// (2) Target conflict: with the daemon stopped, take a valid backup, then aim a restore at a
	//     destination that already holds a .qompack.
	shutdownIfReachable(t, p.Root)
	create := runBackupJSON(t, b, p.Root,
		[]string{"backup", "create", "--project", p.Root, "--id", "before", "--json"}, p.Env)
	require.Equal(t, 0, create.code, "backup create must succeed with the daemon stopped: %s", create.stderr)

	dest := newProject(t, "occupied").Root
	require.NoError(t, os.MkdirAll(paths.Long(paths.Of(dest).Dot), 0o700))
	sentinel := filepath.Join(paths.Of(dest).Dot, "sentinel")
	require.NoError(t, os.WriteFile(paths.Long(sentinel), []byte("do not touch"), 0o600))

	conflict := runBackupJSON(t, b, p.Root,
		[]string{"backup", "restore", "--project", p.Root, "--id", "before", "--destination", dest, "--json"}, p.Env)
	require.NotEqual(t, 0, conflict.code, "restore must refuse an occupied destination")
	require.Contains(t, conflict.res.Error, "exist",
		"the refusal must name the existing destination: %q", conflict.res.Error)
	got, err := os.ReadFile(paths.Long(sentinel))
	require.NoError(t, err)
	require.Equal(t, "do not touch", string(got), "a refused restore must leave the occupied destination byte-for-byte intact")

	rec.Outcome = OutcomeExplicitIncomplete
	rec.Reason = "backup create refuses beside a live daemon (telling the operator to stop it) and creates no artifact; restore refuses an occupied destination and leaves it untouched. The product reports both boundaries rather than proceeding."
	rec.Detail = fmt.Sprintf("live_refusal=%q; conflict_refusal=%q", live.res.Error, conflict.res.Error)
}

// TestV6Backup_CorruptedAndUnknownSchemaRefusedWithoutOverwriting proves a corrupted backup and an
// unknown-schema manifest both fail restore without ever publishing a destination store.
func TestV6Backup_CorruptedAndUnknownSchemaRefusedWithoutOverwriting(t *testing.T) {
	b := assembledBundle(t)
	p := newProject(t, "source")
	t.Cleanup(func() {
		shutdownIfReachable(t, p.Root)
		requireNoOrphan(t, p.Root)
	})

	rec := newRecord(t, "v6_backup_corrupt_or_unknown_schema_refused")
	rec.Phase = PhaseLifecycle
	rec.Boundary = "restore of a corrupted backup and of an unknown-schema manifest"
	rec.SeedMethod = "packaged hooks seed a store; daemon stopped; a valid backup is taken; then one tree file is tampered and, separately, the manifest schema is bumped beyond this build; each restore is aimed at its own fresh destination"
	rec.Outcome = OutcomeFailed
	rec.Reason = "a corrupted or unknown-schema restore did not fail, or it published a destination store"
	defer func() { writeRecord(t, rec) }()

	const seed = core.SessionID("sess-fault-v6-backup-corrupt")
	seedSession(t, b, p, seed)
	shutdownIfReachable(t, p.Root)

	create := runBackupJSON(t, b, p.Root,
		[]string{"backup", "create", "--project", p.Root, "--id", "good", "--json"}, p.Env)
	require.Equal(t, 0, create.code, "backup create must succeed: %s", create.stderr)
	require.NotNil(t, create.res.Manifest)
	require.NotEmpty(t, create.res.Manifest.Files)

	treeDir := filepath.Join(paths.Of(p.Root).Backup, "good", "tree")
	manifestPath := filepath.Join(paths.Of(p.Root).Backup, "good", "manifest.json")

	// (1) Corrupt one tree file: restore must refuse and publish nothing.
	victim := filepath.Join(treeDir, filepath.FromSlash(create.res.Manifest.Files[0].Name))
	require.NoError(t, os.WriteFile(paths.Long(victim), []byte("corrupted bytes of a different length"), 0o600))
	destCorrupt := newProject(t, "dest-corrupt").Root
	corrupt := runBackupJSON(t, b, p.Root,
		[]string{"backup", "restore", "--project", p.Root, "--id", "good", "--destination", destCorrupt, "--json"}, p.Env)
	require.NotEqual(t, 0, corrupt.code, "a corrupted backup must not restore")
	requireNoPublishedStore(t, destCorrupt)

	// (2) Unknown schema: bump the manifest version beyond this build. Restore must refuse before it
	//     ever touches the destination.
	require.NoError(t, os.WriteFile(paths.Long(manifestPath), []byte(`{"version":999,"id":"good","files":[]}`), 0o600))
	destSchema := newProject(t, "dest-schema").Root
	schema := runBackupJSON(t, b, p.Root,
		[]string{"backup", "restore", "--project", p.Root, "--id", "good", "--destination", destSchema, "--json"}, p.Env)
	require.NotEqual(t, 0, schema.code, "an unknown-schema manifest must not restore")
	requireNoPublishedStore(t, destSchema)

	rec.Outcome = OutcomeExplicitIncomplete
	rec.Reason = "restore refuses a corrupted backup and an unknown-schema manifest, and in neither case does it publish a destination .qompack; the failures are reported, not swept."
	rec.Detail = fmt.Sprintf("corrupt_refusal=%q; schema_refusal=%q", corrupt.res.Error, schema.res.Error)
}

// readAll reads a whole file the package's long-path way, failing the test on error.
func readAll(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(paths.Long(p))
	require.NoError(t, err, "reading %s", p)
	return b
}

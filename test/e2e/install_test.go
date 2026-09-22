package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/store"
	"github.com/qompack/qompack/internal/testutil"
)

// Installation, upgrade and removal rehearsed against the REAL host CLI (SP-17 Commit 8;
// acceptance rows SP17-M7-01 installed half, SP17-M7-05, SP17-M7-06 uninstall half).
//
// Everything here runs against `claude` on PATH with CLAUDE_CONFIG_DIR pointed at a disposable temp
// directory, so the user's own installation is never written (R8-2) — and the fingerprint guard
// asserts that rather than assuming it. No invocation starts a model session (R8-1): the only
// subcommands are `plugin validate|install|update|uninstall|list` and `plugin marketplace
// add|update`, each with a bound and a closed stdin.
//
// The archive form of a bundle (`devtool bundle --archive`) exists on this tree (Task 7) but is
// not driven here: the rehearsal installs from the assembled DIRECTORY. Release-check wiring
// of these three names is deliverable 7 of the follow-up, not this file.

// installSession and installMarker are this file's session identity and the content string every
// recall assertion searches for. The marker is deliberately long and unusual: recall is a real
// retrieval over real sketches, and a short token would be answerable by accident.
const (
	installSession = core.SessionID("sess-e2e-sp17-install")
	installMarker  = "qompack-sp17-install-rehearsal-marker"
)

// installProjectFiles is the source tree the rehearsal's project carries. It exists so the
// byte-identity check after uninstall has something to be about: a project with no files of its own
// cannot distinguish "nothing was touched" from "there was nothing to touch".
var installProjectFiles = map[string]string{
	"src/app.ts":           "export const app = () => 1;\n",
	"src/lib/util.ts":      "export const util = () => 2;\n",
	"README.md":            "# rehearsal project\n",
	"docs/design.md":       "design notes\n",
	"src/my folder/a b.ts": "export const spaced = 3;\n",
}

// TestInstall_HostCLIInstallUpgradeUninstall is the install/upgrade/uninstall matrix, executed on
// this host in one serial pass over one disposable Claude home and one real project.
//
// The subtests share state on purpose and run in order: an upgrade is only meaningful over an
// install, and an uninstall is only meaningful over both. Each writes its own evidence record, so a
// subtest that fails still leaves the matrix able to say which step stopped.
func TestInstall_HostCLIInstallUpgradeUninstall(t *testing.T) {
	bundles := hostBundles(t)
	base, next := bundles[installBaseVersion], bundles[installNextVersion]

	// R8-2: the fingerprint is taken before the first host invocation — including `claude
	// --version`, which findClaudeCLI runs under a temp CLAUDE_CONFIG_DIR.
	fingerprint := realClaudeConfigFingerprint(t)
	t.Cleanup(func() {
		require.Equal(t, fingerprint, realClaudeConfigFingerprint(t),
			"R8-2: the user's real ~/.claude must be byte-identical across every host invocation")
	})

	if cli, _ := findClaudeCLI(); cli == "" {
		skipInstallHostCases(t, base.ID)
	}

	// The bundle's own identity, before a host has seen it: every line of checksums.txt verifies
	// against the assembled tree, and BUNDLE.json agrees with the plugin manifest inside it.
	require.NotZero(t, verifyChecksumsFile(t, base.Dir))
	require.Equal(t, base.ID.Version, bundlePluginVersion(t, base.Dir),
		".claude-plugin/plugin.json and BUNDLE.json must name one version")

	home := filepath.Join(t.TempDir(), "claude-home")
	require.NoError(t, os.MkdirAll(paths.Long(home), 0o700), "the disposable host home starts empty")
	market := writeMarketplace(t, filepath.Dir(base.Dir), base.DirBase)

	root, projHome := newInstallProject(t)
	t.Cleanup(func() { e2eShutdownIfReachable(t, root) })
	// The pre-install snapshot: content digests of everything OUTSIDE .qompack/ and .git/. R8-3 is
	// why .qompack is excluded — it is the user's recorded data, not the plugin's to remove — and
	// .git is excluded because git itself rewrites its index and logs whenever anything runs.
	preInstall := hashTree(t, root, ".qompack", ".git")
	require.NotEmpty(t, preInstall)

	t.Run("validate_the_bundle_strictly", func(t *testing.T) {
		res := runClaudePlugin(t, home, "plugin", "validate", base.Dir, "--strict", "--json")
		requireHostOK(t, "install_validate_bundle_strict", capInstall, base.ID, res)
		var report struct {
			Success  bool `json:"success"`
			Strict   bool `json:"strict"`
			Manifest struct {
				Type     string            `json:"type"`
				Errors   []json.RawMessage `json:"errors"`
				Warnings []json.RawMessage `json:"warnings"`
			} `json:"manifest"`
		}
		require.NoError(t, json.Unmarshal([]byte(res.Stdout), &report), "--json must be JSON: %s", res.Stdout)
		require.True(t, report.Success, "strict validation reported failure: %s", res.Stdout)
		require.True(t, report.Strict)
		require.Equal(t, "plugin", report.Manifest.Type)
		require.Empty(t, report.Manifest.Errors)
		require.Empty(t, report.Manifest.Warnings, "under --strict a warning IS a failure")
		recordInstallVerified(t, "install_validate_bundle_strict", capInstall, base.ID,
			"claude plugin validate --strict --json: exit 0, success true, zero errors and zero warnings")
	})

	t.Run("install_into_the_user_scope", func(t *testing.T) {
		add := runClaudePlugin(t, home, "plugin", "marketplace", "add", market)
		requireHostOK(t, "install_marketplace_user_scope", capInstall, base.ID, add)

		res := runClaudePlugin(t, home, "plugin", "install", "qompack@"+installMarketplaceName, "-s", "user", "-y")
		requireHostOK(t, "install_marketplace_user_scope", capInstall, base.ID, res)

		// The host's own record of the installation, in the three places it keeps one.
		var settings struct {
			EnabledPlugins map[string]bool `json:"enabledPlugins"`
		}
		readJSONFile(t, filepath.Join(home, "settings.json"), &settings)
		require.True(t, settings.EnabledPlugins["qompack@"+installMarketplaceName],
			"enabledPlugins must name the plugin as qompack@<marketplace>: %+v", settings.EnabledPlugins)

		cache := installCacheDir(home, base.ID.Version)
		require.DirExists(t, paths.Long(cache), "the payload cache must hold the installed version")
		verified := verifyChecksumsFile(t, cache)
		require.NotZero(t, verified, "every checksums.txt line must verify inside the cache")
		require.Equal(t, base.ID.Version, bundlePluginVersion(t, cache),
			"the cached plugin.json version must equal BUNDLE.json's")

		listed := installListPlugins(t, home, "install_marketplace_user_scope", capInstall, base.ID)
		require.Len(t, listed, 1, "exactly one plugin is installed in this disposable home")
		require.Equal(t, "qompack@"+installMarketplaceName, listed[0].ID)
		require.Equal(t, base.ID.Version, listed[0].Version)
		require.True(t, listed[0].Enabled, "an installed plugin must be enabled")
		require.Equal(t, "user", listed[0].Scope)

		recordInstallVerified(t, "install_marketplace_user_scope", capInstall, base.ID,
			fmt.Sprintf("claude plugin marketplace add + install qompack@%s -s user: enabledPlugins "+
				"names it, the cache holds the bundle with %d checksums.txt lines verifying, and "+
				"plugin list --json reports it enabled at user scope",
				installMarketplaceName, verified))
	})

	t.Run("the_launcher_resolves_in_the_installed_location", func(t *testing.T) {
		launcher := installedLauncher(home, base.ID.Version)
		require.FileExists(t, paths.Long(launcher),
			"${CLAUDE_PLUGIN_ROOT}/bin/qompack is the path the manifest names")

		sum, err := installFileSHA256(launcher)
		require.NoError(t, err)
		require.Equal(t, base.ID.BinarySHA256, sum,
			"the installed launcher must be the bundle's own binary, byte for byte")

		stdout, stderr, code := Run(t, launcher, []string{"version"}, nil, installEnvFor(root, projHome))
		require.Equal(t, 0, code, "the installed launcher must run: %s", stderr)
		require.Equal(t, base.ID.Version, strings.TrimSpace(string(stdout)),
			"the launcher must report the version its bundle was stamped with")

		stdout, stderr, code = Run(t, launcher, []string{"status", "--json"}, nil, installEnvFor(root, projHome))
		require.Equal(t, 0, code, "status --json from the installed location: %s", stderr)
		installAssertStatusJSON(t, stdout)

		// Task 2's platform matrix already established how ${CLAUDE_PLUGIN_ROOT}/bin/qompack
		// resolves under each Windows shell (commit2-evidence.md, shell-* records); this row adds
		// only the fact those records could not have: the path is the HOST'S OWN cache directory.
		recordInstallVerified(t, "install_launcher_resolves_in_cache", capInstall, base.ID,
			"the installed "+launcher+" runs, reports version "+base.ID.Version+
				" and hashes to the bundle's binary_sha256; shell resolution is commit2-evidence.md's")
	})

	t.Run("upgrade_to_the_next_version", func(t *testing.T) {
		// A session on the OLD version first, so the upgrade has real data to carry across.
		oldLauncher := installedLauncher(home, base.ID.Version)
		installDriveSession(t, oldLauncher, root, projHome, installSession, installMarker)
		e2eShutdownIfReachable(t, root)
		dataBefore := hashTree(t, filepath.Join(root, ".qompack"), "logs", "run", "tmp", "metrics")
		require.NotEmpty(t, dataBefore, "the session must have left objects and index lines behind")

		// Republish: the same marketplace now points at the next bundle directory.
		writeMarketplace(t, filepath.Dir(next.Dir), next.DirBase)
		upd := runClaudePlugin(t, home, "plugin", "marketplace", "update", installMarketplaceName)
		requireHostOK(t, "upgrade_marketplace_republish", capUpgrade, next.ID, upd)
		res := runClaudePlugin(t, home, "plugin", "update", "qompack", "-s", "user", "-y")
		requireHostOK(t, "upgrade_marketplace_republish", capUpgrade, next.ID, res)

		listed := installListPlugins(t, home, "upgrade_marketplace_republish", capUpgrade, next.ID)
		require.Len(t, listed, 1)
		require.Equal(t, next.ID.Version, listed[0].Version, "the new version must be the enabled one")
		require.True(t, listed[0].Enabled)

		// The old cache directory: host docs say an old version is kept for about fourteen days.
		// Which of the two the host did is RECORDED, never asserted — it is the host's policy.
		oldCacheKept := e2eFileExists(paths.Long(installCacheDir(home, base.ID.Version)))
		policy := "the old cache directory was REMOVED by the host"
		if oldCacheKept {
			policy = "the old cache directory was RETAINED by the host (docs: about fourteen days)"
		}

		newLauncher := installedLauncher(home, next.ID.Version)
		sum, err := installFileSHA256(newLauncher)
		require.NoError(t, err)
		require.Equal(t, next.ID.BinarySHA256, sum)
		require.NotEqual(t, base.ID.BinarySHA256, next.ID.BinarySHA256,
			"two versions of one bundle must not be the same binary")

		// The user's data crossed the upgrade untouched: the host copies a bundle, it does not
		// migrate a project (ARCH §3.3 — product data never lives in the install directory).
		require.Equal(t, dataBefore, hashTree(t, filepath.Join(root, ".qompack"), "logs", "run", "tmp", "metrics"),
			".qompack/ must be byte-identical across an upgrade")

		// And the new binary recovers the old data: fsck finds no errors, and recall answers with
		// content the OLD version recorded.
		report := installFsck(t, newLauncher, root, projHome)
		require.Equal(t, 0, report.Exit, "fsck over data written by the previous version: %s",
			installFsckFailures(report))
		hits := installRecallHashes(t, newLauncher, root, projHome, installMarker)
		require.NotEmpty(t, hits, "the upgraded binary must recall what the previous one recorded")
		e2eShutdownIfReachable(t, root)

		recordInstallVerified(t, "upgrade_marketplace_republish", capUpgrade, next.ID,
			fmt.Sprintf("claude plugin marketplace update + plugin update qompack -s user: %s -> %s "+
				"enabled, %s, .qompack/ byte-identical across the upgrade, fsck exit 0 and recall "+
				"answered %d hit(s) through the new launcher",
				base.ID.Version, next.ID.Version, policy, len(hits)))
	})

	t.Run("uninstall_keeping_the_data", func(t *testing.T) {
		res := runClaudePlugin(t, home, "plugin", "uninstall", "qompack", "-s", "user", "--keep-data", "-y")
		requireHostOK(t, "uninstall_keep_data", capUninstall, next.ID, res)

		var settings struct {
			EnabledPlugins map[string]bool `json:"enabledPlugins"`
		}
		readJSONFile(t, filepath.Join(home, "settings.json"), &settings)
		require.NotContains(t, settings.EnabledPlugins, "qompack@"+installMarketplaceName,
			"the enabledPlugins entry must be gone")
		require.Empty(t, installListPlugins(t, home, "uninstall_keep_data", capUninstall, next.ID), "plugin list must report nothing installed")

		// What the host did with the payload and the data directory, recorded rather than asserted.
		cacheKept := e2eFileExists(paths.Long(installCacheDir(home, next.ID.Version)))
		dataDir := filepath.Join(home, "plugins", "data")
		dataKept := e2eFileExists(paths.Long(dataDir))
		recordInstallVerified(t, "uninstall_keep_data", capUninstall, next.ID,
			fmt.Sprintf("claude plugin uninstall qompack -s user --keep-data: exit 0, enabledPlugins "+
				"entry gone, plugin list empty; payload cache retained=%v, plugins/data present=%v "+
				"(this host never created a data directory: the plugin was installed, never run in "+
				"a live session)", cacheKept, dataKept))
	})

	t.Run("uninstall_by_default_and_then_delete_the_data_by_hand", func(t *testing.T) {
		// A second disposable home, so the default path is judged against a clean install rather
		// than against whatever the --keep-data run left behind.
		home2 := filepath.Join(t.TempDir(), "claude-home-default")
		require.NoError(t, os.MkdirAll(paths.Long(home2), 0o700))
		add := runClaudePlugin(t, home2, "plugin", "marketplace", "add", market)
		requireHostOK(t, "uninstall_default", capUninstall, next.ID, add)
		ins := runClaudePlugin(t, home2, "plugin", "install", "qompack@"+installMarketplaceName, "-s", "user", "-y")
		requireHostOK(t, "uninstall_default", capUninstall, next.ID, ins)
		require.Len(t, installListPlugins(t, home2, "uninstall_default", capUninstall, next.ID), 1)

		res := runClaudePlugin(t, home2, "plugin", "uninstall", "qompack", "-s", "user", "-y")
		requireHostOK(t, "uninstall_default", capUninstall, next.ID, res)

		var settings struct {
			EnabledPlugins map[string]bool `json:"enabledPlugins"`
		}
		readJSONFile(t, filepath.Join(home2, "settings.json"), &settings)
		require.NotContains(t, settings.EnabledPlugins, "qompack@"+installMarketplaceName)
		require.Empty(t, installListPlugins(t, home2, "uninstall_default", capUninstall, next.ID))
		cacheKept := e2eFileExists(paths.Long(installCacheDir(home2, next.ID.Version)))

		// R8-3: the uninstall never touched the project, and it never touched .qompack/. The
		// project tree OUTSIDE .qompack/ and .git/ is byte-identical to the pre-install snapshot.
		require.Equal(t, preInstall, hashTree(t, root, ".qompack", ".git"),
			"uninstall must leave the project tree outside .qompack/ byte-identical")
		objects := paths.Of(root).Objects
		require.DirExists(t, paths.Long(objects), "R8-3: .qompack/ is the user's data and survives uninstall")
		require.NotEmpty(t, hashTree(t, objects), "the recorded objects must still be there")

		// The documented "delete my data" choice is a MANUAL step, performed here and verified.
		// Plan §4 is explicit that this is not secure physical erasure across backups or media: it
		// removes the product's own directory from this project and claims nothing further.
		// Nothing of ours may still hold a handle when the operator deletes their data: a daemon
		// an earlier step brought up holds append handles on index/*.jsonl, and on Windows that is
		// a sharing violation rather than a permission error.
		e2eShutdownIfReachable(t, root)
		require.NoError(t, os.RemoveAll(paths.Long(filepath.Join(root, ".qompack"))))
		require.Equal(t, preInstall, hashTree(t, root, ".git"),
			"after the manual deletion the project tree must equal the pre-install snapshot exactly")

		recordInstallVerified(t, "uninstall_default", capUninstall, next.ID,
			fmt.Sprintf("claude plugin uninstall qompack -s user (no --keep-data): exit 0, "+
				"enabledPlugins entry gone, plugin list empty; payload cache retained=%v; the "+
				"project tree outside .qompack/ and .git/ is byte-identical to the pre-install "+
				"snapshot, .qompack/ survives with its objects, and the documented manual deletion "+
				"then restores the tree to its pre-install state exactly", cacheKept))
	})
}

// rollbackSession is the rollback rehearsal's own session identity, distinct from installSession so
// the two tests never share a draft file or a registry entry.
const rollbackSession = core.SessionID("sess-e2e-sp17-rollback")

// TestRollbackRehearsal_BeforeAndAfterTheFirstNewFormatWrite is SP17-M7-05 executed on real data:
// projects recorded by the real launcher, backed up, and rolled back in rehearsal both before and
// after the first new-format write — then the restored root opened by the installed binary.
//
// It rehearses TWO projects. A project that has sealed a checkpoint is restored through
// RestoreBackup, which writes §7.4 artifacts with paths.CreateNew and the two append-only
// logs with paths.RestoreLog (commit 6, ada54d1 + round 2). The second project, recorded
// without a PreCompact seal, still carries the
// daemon-alive refusal, the after-first-new-write drill and identity parity through the
// installed launcher — distinct properties, not a workaround for a restore that now succeeds.
func TestRollbackRehearsal_BeforeAndAfterTheFirstNewFormatWrite(t *testing.T) {
	ctx := context.Background()
	b := hostBundle(t)
	bin, where := installedOrBundledLauncher(t, b, capRollback,
		"rollback_sealed_checkpoint_restore",
		"rollback_before_first_new_write",
		"rollback_after_first_new_write",
		"rollback_restored_root_reads_through_the_launcher")

	// ── A. a project that has sealed a checkpoint ───────────────────────────────────────────────
	sealed := testutil.NewProject(t, testutil.WithGit(), testutil.WithFiles(installProjectFiles))
	t.Cleanup(func() { e2eShutdownIfReachable(t, sealed.Root) })
	installDriveSession(t, bin, sealed.Root, sealed.Home(), rollbackSession, installMarker)
	arts := cpCheckpointArtifacts(t, sealed.Root)
	require.NotEmpty(t, arts, "PreCompact must have sealed a checkpoint")
	liveCkpt := map[string]string{}
	for _, name := range arts {
		sum, sumErr := installFileSHA256(filepath.Join(paths.Of(sealed.Root).Checkpoints, name))
		require.NoError(t, sumErr)
		liveCkpt[name] = sum
	}
	e2eShutdownIfReachable(t, sealed.Root)

	_, ms := installMigrator(t, sealed)
	installImportAndBackup(t, ms, installBackupID)
	sealedRestore := filepath.Join(t.TempDir(), "restore-sealed")
	sealedDrill, err := ms.RehearseRollback(ctx, store.RollbackOptions{
		Phase: store.RollbackBeforeFirstNewWrite, BackupID: installBackupID,
		RestoreRoot: sealedRestore, StopWriters: installStopWriters(sealed.Root),
	})
	require.NoError(t, err, "a drill that cannot pass is a RESULT, not an error")
	require.True(t, sealedDrill.OK, "refusal: %s", sealedDrill.Refusal)
	require.True(t, sealedDrill.BackupVerified)
	require.True(t, sealedDrill.ReaderProved)
	require.True(t, sealedDrill.WritersStopped)
	require.False(t, sealedDrill.AutomaticDowngrade, "no drill may promise an automatic downgrade")
	require.True(t, sealedDrill.EvidenceRetained)
	for name, want := range liveCkpt {
		got, sumErr := installFileSHA256(filepath.Join(paths.Of(sealedRestore).Checkpoints, name))
		require.NoError(t, sumErr, "the restored root must hold checkpoint %s", name)
		require.Equal(t, want, got, "restored checkpoint %s must match the live artifact byte for byte", name)
	}
	recordInstallVerified(t, "rollback_sealed_checkpoint_restore", capRollback, b.ID,
		"RehearseRollback over a project that sealed a checkpoint: OK, backup verified, reader "+
			"proved, writers stopped, automatic_downgrade false, evidence retained; the restored "+
			"root holds the checkpoint artifact byte-for-byte. Fixed in commit 6 (ada54d1, "+
			"commit6-evidence.md row 19) and its round 2: RestoreBackup writes protected artifacts "+
			"through paths.CreateNew and the append-only logs through paths.RestoreLog, so the "+
			"restored root can seal again (internal/store/backup.go RestoreBackup).")

	// ── B. the same recording without a PreCompact seal ─────────────────────────────────────────
	p := testutil.NewProject(t, testutil.WithGit(), testutil.WithFiles(installProjectFiles))
	t.Cleanup(func() { e2eShutdownIfReachable(t, p.Root) })
	installDriveSessionWithoutCheckpoint(t, bin, p.Root, p.Home(), rollbackSession, installMarker)
	require.NotEmpty(t, obsToolUseLines(p.Root), "the session must have produced index lines")
	require.Empty(t, cpCheckpointArtifacts(t, p.Root), "this project deliberately seals no checkpoint")
	e2eShutdownIfReachable(t, p.Root)

	// Import and backup, then close the handle before any daemon starts (single-writer).
	_, mImport, closeImport := installOpenMigrator(t, p)
	installImportAndBackup(t, mImport, installBackupID)
	closeImport()
	stopWriters := installStopWriters(p.Root)

	// ── the daemon ALIVE: the drill refuses as a RESULT ─────────────────────────────────────────
	installRunHook(t, bin, []string{"session-start"},
		installSessionStartPayload(t, p.Root, rollbackSession), installEnvFor(p.Root, p.Home()))
	e2eWaitDaemonUp(t, p.Root)
	_, mRefuse, closeRefuse := installOpenMigrator(t, p)
	refused, err := mRefuse.RehearseRollback(ctx, store.RollbackOptions{
		Phase: store.RollbackBeforeFirstNewWrite, BackupID: installBackupID,
		RestoreRoot: filepath.Join(t.TempDir(), "restore-refused"), StopWriters: stopWriters,
	})
	closeRefuse()
	require.NoError(t, err, "a drill that cannot pass is a RESULT, not an error")
	require.False(t, refused.OK, "the writer handoff cannot complete beside a live daemon")
	require.False(t, refused.WritersStopped)
	require.Contains(t, refused.Refusal, "could not be stopped")
	require.False(t, refused.AutomaticDowngrade, "no drill may promise an automatic downgrade")
	require.Len(t, rollbackDrillLines(t, p.Root), 1, "the refusal is recorded, not swallowed")

	// ── the daemon stopped: open the long-lived handle, then the before-phase drill passes ────
	e2eShutdownIfReachable(t, p.Root)
	s, m, closeRest := installOpenMigrator(t, p)
	t.Cleanup(closeRest)
	pre, err := m.RehearseRollback(ctx, store.RollbackOptions{
		Phase: store.RollbackBeforeFirstNewWrite, BackupID: installBackupID,
		RestoreRoot: filepath.Join(t.TempDir(), "restore-pre"), StopWriters: stopWriters,
	})
	require.NoError(t, err)
	require.True(t, pre.OK, "refusal: %s", pre.Refusal)
	require.True(t, pre.WritersStopped)
	require.True(t, pre.BackupVerified)
	require.True(t, pre.ReaderProved, "the restored backup must actually read back")
	require.Len(t, pre.RetainedLegacyIDs, installLegacyRecords, "old ids must keep resolving")
	require.Empty(t, pre.UnreadableByOldReader)
	require.False(t, pre.AutomaticDowngrade)
	require.True(t, pre.EvidenceRetained)
	require.Len(t, rollbackDrillLines(t, p.Root), 2)

	// ── after the first new-format write, against the actual new artifact ────────────────────
	_, err = m.Cutover(ctx, store.CutoverOptions{BackupID: installBackupID, StopLegacyWriter: stopWriters})
	require.NoError(t, err)
	written, err := s.PutBytes(ctx, []byte("new-format observation envelope for "+installMarker+"\n"),
		store.PutOptions{Tool: "FileRead", Path: "src/new-format.ts"})
	require.NoError(t, err)
	nw, err := m.RecordNewFormatWrite(ctx, written.Root.Hash, "legacy-001")
	require.NoError(t, err)
	require.True(t, nw.First, "this is the project's first new-format write")

	post, err := m.RehearseRollback(ctx, store.RollbackOptions{
		Phase: store.RollbackAfterFirstNewWrite, BackupID: installBackupID,
		RestoreRoot: filepath.Join(t.TempDir(), "restore-post"), StopWriters: stopWriters,
	})
	require.NoError(t, err)
	require.True(t, post.OK, "refusal: %s", post.Refusal)
	require.True(t, post.BackupVerified)
	require.True(t, post.ReaderProved)
	require.Len(t, post.RetainedLegacyIDs, installLegacyRecords,
		"rolling back must not cost a single legacy id")
	require.Len(t, post.UnreadableByOldReader, 1,
		"the write made after the backup is exactly what an older binary cannot read")
	require.Equal(t, written.Root.Hash.String(), post.UnreadableByOldReader[0].Root)
	require.Equal(t, "legacy-001", post.UnreadableByOldReader[0].LegacyID,
		"the write names the old identity it supersedes, so the id still resolves")
	require.False(t, post.AutomaticDowngrade,
		"plan §4: after new writes the answer is a compatible reader or a restore, never a downgrade")
	require.True(t, post.EvidenceRetained)

	// GC cannot collect the material either drill depends on. That property is proved in-package by
	// internal/store's TestGC_CannotCollectMigrationOrRollbackMaterial (backup_test.go:345), which
	// forces retention and re-reads; it is CITED here rather than duplicated, because a second
	// weaker copy of a proof is worse than one.
	require.DirExists(t, paths.Long(paths.Of(p.Root).Migrate))
	require.DirExists(t, paths.Long(paths.Of(p.Root).Backup))

	recordInstallVerified(t, "rollback_before_first_new_write", capRollback, b.ID,
		fmt.Sprintf("RehearseRollback(before-first-new-format-write) on a project recorded by the "+
			"real launcher: refused as a RESULT beside a live daemon (%q), then OK with the daemon "+
			"stopped — backup verified, reader proved, writers stopped, %d legacy ids retained, "+
			"automatic_downgrade false, evidence retained", refused.Refusal, len(pre.RetainedLegacyIDs)))
	recordInstallVerified(t, "rollback_after_first_new_write", capRollback, b.ID,
		fmt.Sprintf("RehearseRollback(after-first-new-format-write) against the actual new artifact: "+
			"OK, %d write enumerated in unreadable_by_old_reader (%s), legacy ids still %d, "+
			"automatic_downgrade false", len(post.UnreadableByOldReader),
			written.Root.Hash.Short(), len(post.RetainedLegacyIDs)))

	// ── the restored root, opened by the INSTALLED binary ──────────────────────────────────────
	// Without the CLI this row is skipped: a verified installed_cli record through the bundled
	// launcher would claim an install that never happened (R8-2).
	if where != "installed" {
		recordInstallSkipped(t, "rollback_restored_root_reads_through_the_launcher", capRollback, b.ID,
			"platform: claude CLI not on PATH")
		return
	}
	restored := pre.RestoreRoot
	require.DirExists(t, paths.Long(filepath.Join(restored, ".qompack")),
		"a restore root is a PROJECT root: dest/.qompack is what gets populated")
	t.Cleanup(func() { e2eShutdownIfReachable(t, restored) })

	stdout, stderr, code := Run(t, bin, []string{"status", "--json"}, nil, installEnvFor(restored, p.Home()))
	require.Equal(t, 0, code, "status --json over the restored root: %s", stderr)
	installAssertStatusJSON(t, stdout)

	report := installFsck(t, bin, restored, p.Home())
	require.Equal(t, 0, report.Exit, "fsck over the restored root reported defects: %s",
		installFsckFailures(report))

	// Identity parity: every recalled hit carries a root identity the live project holds.
	liveRoots := installRootHashes(t, p.Root)
	require.NotEmpty(t, liveRoots)
	hits := installRecallHashes(t, bin, restored, p.Home(), installMarker)
	require.NotEmpty(t, hits, "the restored root must answer recall for content the live project recorded")
	var parity int
	for _, h := range hits {
		if liveRoots[h] {
			parity++
		}
	}
	require.Equal(t, len(hits), parity, "every recalled hit must carry a root identity the live project holds:\nhits=%v", hits)
	e2eShutdownIfReachable(t, restored)

	recordInstallVerified(t, "rollback_restored_root_reads_through_the_launcher", capRollback, b.ID,
		fmt.Sprintf("the restored root opened by the %s launcher: status --json parses, fsck exit 0 "+
			"with no defect row, and MCP recall answered %d hit(s) of which %d carry a root identity "+
			"the live project holds", where, len(hits), parity))
}

// unknownSchemaSession is the degradation rehearsal's own session identity.
const unknownSchemaSession = core.SessionID("sess-e2e-sp17-unknown-schema")

// TestUnknownSchema_NewerThanThisBuildDegradesWithoutRewriting is SP17-M7-06's "unknown schemas
// safely degrade" half (Qompack.md §7.1: "Unknown host/schema environments report
// unsupported/degraded capability instead of guessing").
//
// Four artifacts newer than this build are planted in a project that already holds real recorded
// data, and the one property that binds all four is asserted for each: the build REPORTS the version
// it cannot read and leaves the bytes exactly as it found them. A build that rewrote a newer
// artifact into a shape it understands would be guessing, and it would destroy the evidence that a
// newer plugin ever wrote there.
//
// The config plant comes LAST so the other three artifacts are hashed against a session that
// still has a default configuration. After commit 6 fix round 1 (ada54d1, commit6-evidence.md
// row 8) LoadForCapture applies the same versioned-section reset Load does: capture CONTINUES,
// a daemon comes up, and state/config-violations.json names the reset.
func TestUnknownSchema_NewerThanThisBuildDegradesWithoutRewriting(t *testing.T) {
	b := hostBundle(t)
	fingerprint := realClaudeConfigFingerprint(t)
	t.Cleanup(func() {
		require.Equal(t, fingerprint, realClaudeConfigFingerprint(t),
			"R8-2: the user's real ~/.claude must be byte-identical across every host invocation")
	})
	if cli, _ := findClaudeCLI(); cli == "" {
		skipUnknownSchemaCases(t, b.ID)
	}
	bin, _ := installedOrBundledLauncher(t, b, capUnknownSchema,
		"unknown_schema_config_settings_version",
		"unknown_schema_checkpoint_artifact",
		"unknown_schema_capture_sidecar",
		"unknown_schema_delivery_seal_v2")

	root, home := newInstallProject(t)
	t.Cleanup(func() { e2eShutdownIfReachable(t, root) })
	installDriveSession(t, bin, root, home, unknownSchemaSession, installMarker)
	e2eShutdownIfReachable(t, root)

	l := paths.Of(root)
	planted := map[string]string{}
	plant := func(path string, body []byte) {
		t.Helper()
		require.NoError(t, os.MkdirAll(paths.Long(filepath.Dir(path)), 0o700))
		require.NoError(t, os.WriteFile(paths.Long(path), body, 0o600))
		sum, err := installFileSHA256(path)
		require.NoError(t, err)
		planted[path] = sum
	}

	// (a) a checkpoint artifact declaring a schema version above this build's, with no MANIFEST
	//     line: "written by a newer plugin", which is a support gap and not an orphan.
	ckptPath := filepath.Join(l.Checkpoints, "9999-newer-plugin.json")
	plant(ckptPath, []byte(fmt.Sprintf(`{"version":%d,"seq":9999,"session":%q}`+"\n",
		checkpoint.SchemaVersion+1, unknownSchemaSession)))

	// (b) a capture sidecar from a newer build: readable, degraded, never repaired — sidecars are
	//     evidence, and `op` is a prompt delivery so the stage-1 gap rule does not apply.
	//
	//     It is planted where every build writes a sidecar — store.CaptureSidecarPath, i.e.
	//     records/captures/<2 hex>/<digest>.json, the only layout since a6faab0 — under a
	//     digest-shaped observation id, because that is where a newer build's sidecar would be.
	//     A file dropped at records/captures/ itself is no build's sidecar: V6's bounded publication
	//     audit (80a3e04) walks the two fixed fanout levels and reports such a file as an unexpected
	//     entry, which is a different question from the one this row asks. Planted here, the newer
	//     schema is actually READ by both the captures row and the publication audit.
	newerObs := core.ObservationID(core.HashBytes("qompack.e2e.unknown-schema", []byte("newer-plugin")).String())
	sidecarPath, err := store.CaptureSidecarPath(root, newerObs)
	require.NoError(t, err)
	plant(sidecarPath, []byte(fmt.Sprintf(
		`{"v":%d,"observation_id":%q,"op":"observe.prompt","published":false,"outcome":"ok"}`+"\n",
		store.CaptureSidecarVersion+1, newerObs)))

	// A whole session over both: every hook exits 0, the daemon still comes up, and recording
	// continues — a newer artifact beside the live data is a support gap, not a stop.
	installDriveSession(t, bin, root, home, unknownSchemaSession, installMarker)
	e2eShutdownIfReachable(t, root)

	// (c) a v2 delivery position seal, classified by the reader path rather than guessed at. An
	//     OLDER binary's refusal of a v2 seal is internal/daemon's own rollback row
	//     (delivery_seal_format2_test.go), cited here rather than duplicated.
	//
	//     It is planted with the project QUIET and read only by fsck, which writes nothing. The
	//     delivery position is LIVE daemon state, not an archival artifact: a hand-written seal
	//     beside a running daemon makes loadDeliveryPosition refuse the journal and publication
	//     stalls, which would measure a race rather than a reader.
	sealPath := filepath.Join(l.State, "delivery-ack-position.json")
	plant(sealPath, []byte(`{"v":2,"offset":0,"seq":0}`+"\n"))

	report := installFsck(t, bin, root, home)
	require.Equal(t, 0, report.Exit, "a newer artifact is a support gap, not a defect: %s",
		installFsckFailures(report))
	ckptRow := installFsckRow(t, report, "checkpoints")
	require.True(t, ckptRow.OK, "the checkpoints row must stay clean")
	require.Contains(t, strings.Join(ckptRow.Detail, "\n"), "written by a newer plugin",
		"fsck must NAME the artifact it cannot read: %+v", ckptRow.Detail)
	capRow := installFsckRow(t, report, "captures")
	require.True(t, capRow.OK, "the captures row must stay clean")
	require.Contains(t, strings.Join(capRow.Detail, "\n"), "newer than this build",
		"fsck must NAME the sidecar it cannot read: %+v", capRow.Detail)
	sealRow := installFsckRow(t, report, "delivery")
	require.True(t, sealRow.OK)
	require.Contains(t, strings.Join(sealRow.Detail, "\n"), "v2 position document",
		"fsck must classify the seal by its declared version: %+v", sealRow.Detail)

	stdout, stderr, code := Run(t, bin, []string{"doctor", "--project", root, "--json"}, nil, installEnvFor(root, home))
	require.Equal(t, 0, code, "doctor --json: %s", stderr)
	installAssertDoctorJSON(t, stdout, root)

	// (d) a config section whose settingsVersion is newer than this build understands, with a
	//     switch a newer file could set planted beside it so the reset is observable.
	cfgPath := filepath.Join(l.Dot, "config.json")
	plant(cfgPath, []byte(fmt.Sprintf(
		`{"runtime":{"migration":{"settingsVersion":%d,"experiments":{"enabled":true},"reinjection":{"sessionStartCompact":false}}}}`+"\n",
		config.MigrationSettingsVersion+1)))

	// Capture CONTINUES: hooks exit 0, a daemon comes up, the reset is recorded.
	installDriveSession(t, bin, root, home, unknownSchemaSession, installMarker)
	e2eShutdownIfReachable(t, root)

	violations := installConfigViolations(t, root)
	var namedReset bool
	for _, v := range violations {
		if v.Key == "runtime.migration" && strings.Contains(v.Message, "reset") {
			namedReset = true
		}
	}
	require.True(t, namedReset,
		"state/config-violations.json must name the versioned-section reset: %+v", violations)

	// Off the hot path the same file is RESET, with the provenance saying why.
	stdout, stderr, code = Run(t, bin, []string{"config", "print", "--provenance"}, nil,
		installEnvFor(root, home))
	require.Equal(t, 0, code, "config print --provenance: %s", stderr)
	require.Contains(t, string(stdout), "reset: newer settingsVersion",
		"the provenance must name WHY the block holds defaults")

	stdout, stderr, code = Run(t, bin, []string{"config", "print", "--json"}, nil, installEnvFor(root, home))
	require.Equal(t, 0, code, "config print --json: %s", stderr)
	var effective struct {
		Runtime struct {
			Migration struct {
				SettingsVersion int `json:"settingsVersion"`
				Experiments     struct {
					Enabled bool `json:"enabled"`
				} `json:"experiments"`
				Reinjection struct {
					SessionStartCompact bool `json:"sessionStartCompact"`
				} `json:"reinjection"`
			} `json:"migration"`
		} `json:"runtime"`
	}
	require.NoError(t, json.Unmarshal(stdout, &effective), "config print --json must be JSON: %s", stdout)
	require.Equal(t, config.MigrationSettingsVersion, effective.Runtime.Migration.SettingsVersion,
		"the effective settingsVersion is this build's, never the file's")
	require.False(t, effective.Runtime.Migration.Experiments.Enabled,
		"experiments.enabled is gated: Validate restores false on any build, reset or not")
	require.True(t, effective.Runtime.Migration.Reinjection.SessionStartCompact,
		"the ungated sessionStartCompact switch returns to default true only when the block resets")

	// ── nothing the binary ran rewrote a single planted byte ────────────────────────────────────
	for path, want := range planted {
		got, err := installFileSHA256(path)
		require.NoError(t, err, "the planted file must still be there: %s", path)
		require.Equal(t, want, got,
			"a newer-than-this-build artifact must never be rewritten: %s", path)
	}

	recordInstallVerified(t, "unknown_schema_config_settings_version", capUnknownSchema, b.ID,
		fmt.Sprintf("runtime.migration.settingsVersion %d with experiments.enabled true "+
			"(gated; Validate restores false on any build) and reinjection.sessionStartCompact false "+
			"(ungated, default true; build reads settingsVersion %d): LoadForCapture resets the "+
			"block (ada54d1, commit6-evidence.md row 8, capture_load.go:123); "+
			"state/config-violations.json names the reset; the effective block is defaults "+
			"(sessionStartCompact true); hooks exit 0; a daemon comes up; config print --provenance "+
			"still reports reset: newer settingsVersion; .qompack/config.json is byte-identical afterwards",
			config.MigrationSettingsVersion+1, config.MigrationSettingsVersion))
	recordInstallVerified(t, "unknown_schema_checkpoint_artifact", capUnknownSchema, b.ID,
		fmt.Sprintf("a checkpoints/ artifact declaring schema version %d (build reads %d): a whole "+
			"session still records, fsck's checkpoints row stays ok and names it \"written by a newer "+
			"plugin\", and the artifact's bytes are unchanged",
			checkpoint.SchemaVersion+1, checkpoint.SchemaVersion))
	recordInstallVerified(t, "unknown_schema_capture_sidecar", capUnknownSchema, b.ID,
		fmt.Sprintf("a records/captures sidecar at v%d (build reads v%d): readable and degraded — "+
			"fsck's captures row stays ok and names it, and the sidecar is never repaired or swept",
			store.CaptureSidecarVersion+1, store.CaptureSidecarVersion))
	recordInstallVerified(t, "unknown_schema_delivery_seal_v2", capUnknownSchema, b.ID,
		"a v2 delivery position seal: fsck's delivery row classifies it by its declared version and "+
			"leaves the bytes alone; an OLDER binary's refusal of a v2 seal is internal/daemon's own "+
			"rollback row (delivery_seal_format2_test.go), cited here rather than duplicated")
}

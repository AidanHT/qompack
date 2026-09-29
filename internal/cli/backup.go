package cli

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/store"
)

func backupCmds() []Cmd {
	return []Cmd{
		{Name: "backup create", Summary: "create a consistent backup with the daemon stopped", Run: backupRunner("create")},
		{Name: "backup verify", Summary: "verify a named backup with the daemon stopped", Run: backupRunner("verify")},
		{Name: "backup restore", Summary: "restore a named backup into a fresh destination", Run: backupRunner("restore")},
	}
}

func backupRunner(action string) func(context.Context, Env, []string, io.Writer, io.Writer) error {
	return func(ctx context.Context, env Env, args []string, out, errw io.Writer) error {
		return runBackup(ctx, env, action, args, out, errw)
	}
}

type backupReport struct {
	Schema    int                   `json:"schema"`
	Action    string                `json:"action"`
	ID        string                `json:"id"`
	Manifest  *store.BackupManifest `json:"manifest,omitempty"`
	Restore   *store.RestoreProof   `json:"restore,omitempty"`
	Integrity *fsckReport           `json:"integrity,omitempty"`
	Error     string                `json:"error,omitempty"`
}

func runBackup(ctx context.Context, env Env, action string, args []string, out, errw io.Writer) (resultErr error) {
	fs := flag.NewFlagSet("backup "+action, flag.ContinueOnError)
	fs.SetOutput(errw)
	project := fs.String("project", "", "source project root")
	id := fs.String("id", "", "backup identifier")
	dest := fs.String("destination", "", "restore project root; .qompack must not exist")
	asJSON := fs.Bool("json", false, "emit a structured result")
	if err := fs.Parse(args); err != nil {
		return errUsageReported
	}
	if fs.NArg() != 0 || *id == "" || (action == "restore") != (*dest != "") {
		fmt.Fprintln(errw, "qompack backup: --id is required; only restore requires --destination; positional arguments are not accepted")
		return errUsageReported
	}
	report := backupReport{Schema: 1, Action: action, ID: *id}
	// Emit only after the writer and lease defers have completed, so a failed
	// flush or lost lease cannot accompany a successful JSON result.
	defer func() {
		if resultErr != nil {
			report.Error = resultErr.Error()
		}
		if *asJSON {
			resultErr = errors.Join(resultErr, json.NewEncoder(out).Encode(report))
		} else if resultErr == nil {
			_, resultErr = fmt.Fprintf(out, "backup %s: %s succeeded\n", action, *id)
			if action == "restore" {
				_, err := fmt.Fprintln(out, "Point-in-time restore into a separate destination, verified by this build. Source and later writes retained; no automatic downgrade or activation.")
				resultErr = errors.Join(resultErr, err)
			}
		}
	}()
	root := *project
	if root == "" {
		root = resolveProjectRoot(env, nil)
	}
	if root == "" {
		return errors.New("backup: source project is unavailable")
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	// D18, for both ends and before the source is locked: the home directory's .qompack is the
	// user-global layer, which is neither a store to back up nor a destination to restore one into.
	if refused := refuseHomeRoot(env, root); refused != nil {
		return fmt.Errorf("backup: source: %w", refused)
	}
	if action == "restore" {
		if refused := refuseHomeRoot(env, *dest); refused != nil {
			return fmt.Errorf("backup: destination: %w", refused)
		}
	}
	if !isDir(paths.Of(root).Dot) {
		return errors.New("backup: source project has no existing store")
	}
	cfg, _, violations, warnings, err := config.LoadForCapture(config.Env{
		ProjectRoot: root, HomeDir: homeDir(env), Getenv: env.Getenv, Flags: env.Set,
	})
	if err != nil {
		return fmt.Errorf("backup: configuration unavailable: %w", err)
	}
	// Maintenance runs only on the configuration exactly as written. A key the capture loader now
	// drops with a warning (V6 close-out C1.8) used to refuse here through the error above, and it
	// still refuses: backup keeps that stricter admission rather than inheriting the hook path's
	// per-leaf fallback.
	if len(violations) != 0 || len(warnings) != 0 {
		return errors.New("backup: resolve configuration violations and warnings before maintenance")
	}
	ctx, lease, release, err := acquireWriterLease(ctx, root)
	if err != nil {
		return fmt.Errorf("backup: stop the source daemon before maintenance: %w", err)
	}
	defer func() { resultErr = errors.Join(resultErr, release()) }()
	var s store.Store
	if action == "create" {
		s, err = store.Open(root, cfg, store.Deps{Clock: env.Clock, Log: logging.Nop()})
		if err != nil {
			return err
		}
		defer func() { resultErr = errors.Join(resultErr, s.Close()) }()
	}
	maintenance, err := store.NewMaintenance(s, root, store.MaintenanceOptions{
		Cfg: cfg, Clock: env.Clock, WriterLeaseHeld: lease.Heartbeat,
	})
	if err != nil {
		return err
	}
	switch action {
	case "create", "verify":
		var manifest store.BackupManifest
		if action == "create" {
			manifest, err = maintenance.TakeBackup(ctx, *id)
		} else {
			manifest, err = maintenance.VerifyBackupContext(ctx, *id)
		}
		if err == nil {
			report.Manifest = &manifest
			if action == "verify" {
				err = proveBackupByScratchRestore(ctx, maintenance, root, *id, &report)
			}
		}
	case "restore":
		var destination string
		destination, err = filepath.Abs(*dest)
		if err == nil {
			err = restoreAndCheck(ctx, maintenance, *id, destination, &report)
			if err != nil && report.Integrity == nil && report.Restore != nil {
				report.Restore.Note += " " + restoreNoIntegrityNote
			}
		}
	default:
		err = errors.New("backup: unsupported operation")
	}
	return err
}

// restoreIntegrityNote and restoreNoIntegrityNote finish a restore proof's Note with what the command
// did after the reader proof, so the note never contradicts the integrity report beside it: every
// restore used to say the checkpoint chain and delivery seals were NOT covered and to run `fsck
// --seal-check`, while the same response's integrity report had just run it and passed (C1.7).
const (
	restoreIntegrityNote = "The checkpoint chain and the delivery seals were then checked on the " +
		"destination by the integrity report in this response (fsck with the full dual-reader seal check)."
	restoreNoIntegrityNote = "No integrity check ran: the restore stopped before it published a " +
		"destination to check."
	// verifyScratchNote says where verify's proof ran: `backup verify` restores into a scratch
	// destination and removes it afterwards (proveBackupByScratchRestore).
	verifyScratchNote = "backup verify ran this restore into a scratch destination, which it removed " +
		"afterwards; a restore of the same backup makes the same proof and the same checks."
	verifyScratchKeptNote = "backup verify ran this restore into a scratch destination, kept for " +
		"inspection because the proof or the checks failed; a restore of the same backup would fail the same way."
)

// restoreAndCheck restores backup id into destination and runs the packaged integrity scan over it,
// with the full dual-reader seal check. report receives the proof and, once a destination was
// published, the integrity report.
func restoreAndCheck(ctx context.Context, maintenance *store.Maintenance, id, destination string, report *backupReport) error {
	proof, err := maintenance.Restore(ctx, id, destination)
	report.Restore = &proof
	if err != nil {
		return err
	}
	integrity := fsckScanProject(ctx, destination, false, true)
	report.Integrity = &integrity
	proof.Note += " " + restoreIntegrityNote
	if integrity.Exit != ExitOK {
		return errors.New("backup: restored destination retained, but its integrity checks failed; inspect the report")
	}
	return nil
}

// proveBackupByScratchRestore makes `backup verify` judge what `backup restore` judges (F-C49-2).
// Verification used to stop at re-hashing the backup's files, so a backup whose bytes were intact
// but whose store no reader could fully serve verified cleanly and then failed its restore. Verify
// now restores the backup into a scratch destination, makes the same reader proof and runs the same
// integrity scan, and reports both. A scratch restore that passes is removed; one that fails is
// kept, like a failed restore's destination, and the error names it. The backup is only read.
//
// The scratch destination is <root>/.qompack/tmp/verify-<id>-<random>/project (verifyScratchDir),
// never the system temporary directory: 00-ARCHITECTURE.md §13 invariant 7 keeps `$TMPDIR` at large
// out of the product write set, a scratch restore is a full copy of the project's captured prompts
// and tool output, and on Linux /tmp is often tmpfs. The source's tmp/ is inside the write set, on
// the store's own filesystem, left out of every backup (backupSkipDirs), and this command holds the
// source's writer lease while it writes there.
func proveBackupByScratchRestore(ctx context.Context, maintenance *store.Maintenance, root, id string, report *backupReport) error {
	scratch, err := verifyScratchDir(root, id)
	if err != nil {
		return fmt.Errorf("backup: verify: the backup's files verify, but no scratch destination was available "+
			"for the restore proof: %w", err)
	}
	rerr := restoreAndCheck(ctx, maintenance, id, filepath.Join(scratch, "project"), report)
	if rerr != nil {
		if report.Restore != nil {
			report.Restore.Note += " " + verifyScratchKeptNote
			if report.Integrity == nil {
				report.Restore.Note += " " + restoreNoIntegrityNote
			}
		}
		return fmt.Errorf("backup: verify: the backup's files verify, but restoring it fails as a restore "+
			"would (the scratch restore is kept at %s): %w", scratch, rerr)
	}
	report.Restore.Note += " " + verifyScratchNote
	if err := os.RemoveAll(paths.Long(scratch)); err != nil {
		// The backup verified; only the cleanup did not. Say so rather than fail the verification.
		report.Restore.Note += fmt.Sprintf(" The scratch restore at %s could not be removed (%v); "+
			"delete it by hand.", scratch, err)
	}
	return nil
}

// verifyScratchRandBytes is how many random bytes name a verify scratch directory (16 hex digits), the
// same width a restore's staging tree uses (store's maintStagingRoot): two verifies of one backup id
// never share a directory.
const verifyScratchRandBytes = 8

// verifyScratchDir creates backup verify's scratch directory, <root>/.qompack/tmp/verify-<id>-<16
// hex>, and returns it in the ordinary spelling (not paths.Long's) so the reports and errors that
// name it read as a normal path. id is already validated: VerifyBackupContext refused anything else.
func verifyScratchDir(root, id string) (string, error) {
	var b [verifyScratchRandBytes]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	tmp := paths.Of(root).Tmp
	if err := os.MkdirAll(paths.Long(tmp), 0o700); err != nil {
		return "", err
	}
	scratch := filepath.Join(tmp, "verify-"+id+"-"+hex.EncodeToString(b[:]))
	if err := os.Mkdir(paths.Long(scratch), 0o700); err != nil {
		return "", err
	}
	return scratch, nil
}

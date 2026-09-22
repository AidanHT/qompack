package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
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
		}
	case "restore":
		var destination string
		destination, err = filepath.Abs(*dest)
		if err == nil {
			var proof store.RestoreProof
			proof, err = maintenance.Restore(ctx, *id, destination)
			report.Restore = &proof
			if err == nil {
				integrity := fsckScanProject(ctx, destination, false, true)
				report.Integrity = &integrity
				if integrity.Exit != ExitOK {
					err = errors.New("backup: restored destination retained, but its integrity checks failed; inspect the report")
				}
			}
		}
	default:
		err = errors.New("backup: unsupported operation")
	}
	return err
}

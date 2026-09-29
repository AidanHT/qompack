package store

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/paths"
)

// Supported operator MAINTENANCE capability — consistent backup, verification, and
// restore-to-a-fresh-destination of the current engine's store (V6 remediation of V6-RECOVERY-2,
// decisions D-A…D-F). Additive and gate-free: the legacy import/cutover gate guards the MUTATING
// migrate.go path, which this file neither uses nor exposes.
//
// The Maintenance type holds an unexported *Migrator (no LegacySource) and does NOT embed it, so
// Import/Parity/Cutover/RecordNewFormatWrite are unreachable here. Trust boundaries: TakeBackup fails
// closed unless an externally held writer lease is re-confirmed before AND after the copy (D-B);
// Restore refuses an existing destination .qompack, treats manifest ids/names as hostile input,
// streams and re-hashes every byte under a context and size bound, rejects symlink/reparse components
// in the backup and destination paths, proves a same-build READ-ONLY reader, and publishes with an
// atomic no-replace rename (D-C/D-E); the source live tree is never touched (D-D).

var (
	// ErrMaintenanceConfig means NewMaintenance was given no usable config; it never substitutes
	// Defaults().
	ErrMaintenanceConfig = errors.New("store: maintenance needs an explicit config")
	// ErrWriterLeaseRequired means the externally held exclusive writer lease could not be confirmed.
	ErrWriterLeaseRequired = errors.New("store: a backup needs a confirmed exclusive writer lease")
	// ErrBackupID means a backup id is not a simple, safe name.
	ErrBackupID = errors.New("store: unsafe backup id")
	// ErrBackupManifest means a manifest is missing, of an unknown schema, or names an unsafe path.
	ErrBackupManifest = errors.New("store: backup manifest is unusable")
	// ErrRestoreTargetExists means the destination already holds a .qompack (or a publish is racing).
	ErrRestoreTargetExists = errors.New("store: restore destination .qompack already exists")
	// ErrRestoreVacuous means the reader proof read no content root back.
	ErrRestoreVacuous = errors.New("store: restore reader proof read nothing to prove")
)

// errMaintTooLong marks a backup file larger than its manifest size (tamper/corruption), kept
// distinct from a context error so callers do not misreport a cancellation as corruption.
var errMaintTooLong = errors.New("store: file exceeds its manifest size")

const (
	maintMaxManifestBytes = 64 << 20 // bound on the manifest document itself
	maintMaxManifestFiles = 1 << 20  // bound on entry count
	maintMaxFileBytes     = 1 << 34  // 16 GiB per-file ceiling; files are streamed, never buffered whole
	maintCopyBuf          = 1 << 20
)

// MaintenanceOptions configures a Maintenance capability.
type MaintenanceOptions struct {
	// Cfg is REQUIRED (a zero config is refused, never defaulted): a restored store opened under the
	// wrong chunk sizes reads back differently.
	Cfg config.Config
	// Clock stamps durable records; nil means core.SystemClock().
	Clock core.Clock
	// WriterLeaseHeld re-confirms the caller's exclusive writer lease (the daemon singleton lock, kept
	// alive by main's heartbeat). It returns non-nil when the lease is not held. TakeBackup fails
	// closed when it is nil, and re-checks it after the copy.
	WriterLeaseHeld func() error
}

// Maintenance is the gate-free backup / verify / restore surface. It exposes no import/cutover method.
type Maintenance struct {
	m         *Migrator
	leaseHeld func() error
}

// NewMaintenance builds maintenance without the legacy-import gate. A nil store
// supports verification and restore without opening or initializing the source;
// creation requires an open writer store. root is the project root.
func NewMaintenance(s Store, root string, o MaintenanceOptions) (*Maintenance, error) {
	if o.Cfg.Runtime.Migration.SettingsVersion == 0 {
		return nil, ErrMaintenanceConfig
	}
	clk := o.Clock
	if clk == nil {
		clk = core.SystemClock()
	}
	l := paths.Of(root)
	for _, d := range []string{l.Migrate, l.Backup} {
		if !paths.ResolvesInside(root, d) {
			return nil, ErrBackupManifest
		}
		if s == nil {
			break // verification and restore do not initialize the live source
		}
		if err := os.MkdirAll(paths.Long(d), 0o700); err != nil {
			return nil, fmt.Errorf("store: NewMaintenance: mkdir %s: %w", d, err)
		}
	}
	// Built directly rather than through NewMigrator, which refuses a closed gate: this capability is
	// intentionally gate-free and its src is nil (no method here reaches it).
	m := &Migrator{s: s, root: root, l: l, src: nil, batch: defaultImportBatch, clock: clk, cfg: o.Cfg}
	m.copyBackupFile = func(ctx context.Context, src, dst string) (int64, string, error) {
		return maintCreateCopy(ctx, root, src, dst)
	}
	return &Maintenance{m: m, leaseHeld: o.WriterLeaseHeld}, nil
}

func maintCreateCopy(ctx context.Context, root, src, dst string) (int64, string, error) {
	if err := maintNoFollow(root, src); err != nil {
		return 0, "", err
	}
	f, err := paths.OpenShared(src)
	if err != nil {
		return 0, "", err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > maintMaxFileBytes {
		return 0, "", errors.New("store: backup source is not a supported regular file")
	}
	if err := os.MkdirAll(paths.Long(filepath.Dir(dst)), 0o700); err != nil {
		return 0, "", err
	}
	out, err := os.OpenFile(paths.Long(dst), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return 0, "", err
	}
	digest, size, copyErr := maintStreamCopyHash(ctx, f, out, info.Size())
	if copyErr == nil && size != info.Size() {
		copyErr = ErrBackupMoved
	}
	if copyErr == nil {
		copyErr = out.Sync()
	}
	return size, digest, errors.Join(copyErr, out.Close())
}

// TakeBackup makes a consistent backup under id, reusing the engine's backup machinery. It refuses
// unless the id is safe AND the externally held writer lease is confirmed both before and after the
// copy; if the lease is lost mid-copy the artifact is preserved but NOT certified.
func (x *Maintenance) TakeBackup(ctx context.Context, id string) (BackupManifest, error) {
	if err := maintValidateID(id); err != nil {
		return BackupManifest{}, err
	}
	if x.leaseHeld == nil {
		return BackupManifest{}, fmt.Errorf("%w: no lease guard was supplied", ErrWriterLeaseRequired)
	}
	if err := x.leaseHeld(); err != nil {
		return BackupManifest{}, fmt.Errorf("%w: before the copy: %v", ErrWriterLeaseRequired, err)
	}
	if x.m.s == nil {
		return BackupManifest{}, errors.New("store: backup creation requires an open writer store")
	}
	if _, err := os.Lstat(paths.Long(x.m.backupDir(id))); !os.IsNotExist(err) {
		return BackupManifest{}, fmt.Errorf("store: backup destination exists or cannot be inspected: %w", os.ErrExist)
	}
	// Publish this marker before the engine can publish its manifest. A crash
	// or lease loss then leaves durable evidence that verification must refuse.
	pending := x.pendingCertification(id)
	if err := paths.CreateNew(pending, []byte("writer lease certification pending\n")); err != nil {
		return BackupManifest{}, err
	}
	// The marker is only evidence if it is durable before the manifest is: CreateNew syncs its bytes,
	// not its name, and a power cut that kept the manifest and took the marker's name would present an
	// uncertified backup as certified.
	if err := x.m.barriers.DirBarrier(x.m.l.Backup); err != nil {
		return BackupManifest{}, fmt.Errorf("store: backup certification marker is not durable: %w", err)
	}
	man, err := x.m.TakeBackup(ctx, id)
	if err != nil {
		return BackupManifest{}, err
	}
	if lerr := x.leaseHeld(); lerr != nil {
		return BackupManifest{}, fmt.Errorf("%w: lost during the copy; artifact backup/%s is preserved "+
			"for inspection but is NOT certified: %v", ErrWriterLeaseRequired, id, lerr)
	}
	if err := os.Remove(paths.Long(pending)); err != nil {
		return BackupManifest{}, fmt.Errorf("store: backup certification remains pending: %w", err)
	}
	// Certified is what this returns, so the marker's removal is made durable first. Were it lost, the
	// marker would come back and verification would refuse the backup: safe, but not what the operator
	// was told.
	if err := x.m.barriers.DirBarrier(x.m.l.Backup); err != nil {
		return BackupManifest{}, fmt.Errorf("store: backup certification is not yet durable: %w", err)
	}
	return man, nil
}

func (x *Maintenance) pendingCertification(id string) string {
	return filepath.Join(x.m.l.Backup, "."+id+".certification-pending")
}

// VerifyBackup validates the manifest (schema, identity, every path) and then streams and re-hashes
// every file it names against that single snapshot — never a second unvalidated read of the manifest.
//
// Call VerifyBackupContext when verification must honor cancellation.
func (x *Maintenance) VerifyBackup(id string) (BackupManifest, error) {
	return x.VerifyBackupContext(context.Background(), id)
}

// VerifyBackupContext is the cancellable operator verification path.
func (x *Maintenance) VerifyBackupContext(ctx context.Context, id string) (BackupManifest, error) {
	man, err := x.readValidatedManifest(id)
	if err != nil {
		return BackupManifest{}, err
	}
	if err := x.verifyTree(ctx, id, man); err != nil {
		return BackupManifest{}, err
	}
	return man, nil
}

// VerifyBackupAt verifies an existing artifact without opening or changing the
// source store. It needs no runtime configuration because it only reads the
// manifest and hashes the named backup files.
func VerifyBackupAt(ctx context.Context, root, id string) (BackupManifest, error) {
	x := &Maintenance{m: &Migrator{root: root, l: paths.Of(root)}}
	return x.VerifyBackupContext(ctx, id)
}

// RestoreProof is a same-build reader proof over a restored destination. A successful Open alone
// certifies nothing, so it counts payloads actually read back and names what it does not cover.
type RestoreProof struct {
	BackupID    string
	Destination string
	OpenedOK    bool
	// ContentRootsProven is how many restored roots were read back end to end (the store verifies
	// every chunk against its hash on read). It is the non-vacuous core of the proof.
	ContentRootsProven int
	ToolRefsProven     int
	// ToolRefsTombstoned counts tool references whose root a gc tombstone in index/roots.jsonl
	// retired: the store collected it on purpose (an MCP server's ephemeral record, typically), and
	// `qompack fsck` reads the same reference as accounted for. Counted, never read back, and never
	// a restore failure (F-C49-2). A reference nothing accounts for still fails the proof.
	ToolRefsTombstoned int
	// SameBuildOnly is always true; this makes no old-release compatibility claim.
	SameBuildOnly bool
	// CheckpointSealCovered is always false: it describes this proof, and the content reader does not
	// verify checkpoint chains or delivery-seal readability. `qompack backup restore` and `backup
	// verify` check those on the destination with fsck and the full dual-reader seal check, attach
	// that integrity report, and say in Note that they did — so the note never contradicts the
	// report beside it.
	CheckpointSealCovered bool
	Note                  string
}

// RestoreProofNote is what Restore's reader proof covers, and nothing more. A caller that runs
// further checks on the destination appends what they covered.
const RestoreProofNote = "content roots and tool references proven by a same-build read-only reader " +
	"(a tool reference whose root a gc tombstone retired is counted, not read); the reader proof " +
	"itself does not read checkpoint chains or delivery seals. Publication uses the platform's " +
	"atomic no-replace directory rename and refuses unsupported filesystems."

// Restore verifies backup id and publishes it into a FRESH destination .qompack. It uses one
// validated manifest snapshot throughout: refuse an existing destination; stage on the same
// filesystem, streaming and verifying each backup file (rejecting symlink components); re-hash the
// staged bytes; prove a read-only same-build reader; re-hash again; then atomically publish
// without replacing an existing destination. Any failure after staging preserves the staging tree as evidence.
func (x *Maintenance) Restore(ctx context.Context, id, dest string) (RestoreProof, error) {
	proof := RestoreProof{
		BackupID: id, Destination: dest, SameBuildOnly: true, CheckpointSealCovered: false,
		Note: RestoreProofNote,
	}

	man, err := x.readValidatedManifest(id)
	if err != nil {
		return proof, err
	}

	dl := paths.Of(dest)
	if err := maintRefuseExistingDot(dl.Dot); err != nil {
		return proof, err
	}
	// A destination this restore creates is synced into its parent, so the restore reported below
	// cannot vanish with a directory whose own entry a power cut took.
	if err := x.m.barriers.MkdirAll(dest, 0o700); err != nil {
		return proof, fmt.Errorf("store: restore backup %q: destination: %w", id, err)
	}

	staging, err := maintStagingRoot(dest, id)
	if err != nil {
		return proof, fmt.Errorf("store: restore backup %q: staging: %w", id, err)
	}
	stagedDot := paths.Of(staging).Dot

	if err := x.stageRestore(ctx, id, man, staging); err != nil {
		return proof, maintStagePreserved(staging, err)
	}
	if err := x.verifyStaged(ctx, stagedDot, man); err != nil {
		return proof, maintStagePreserved(staging, err)
	}
	if err := x.proveReader(ctx, staging, &proof); err != nil {
		return proof, maintStagePreserved(staging, err)
	}
	// Re-hash after the reader proof: the read-only open must not have changed a staged byte.
	if err := x.verifyStaged(ctx, stagedDot, man); err != nil {
		return proof, maintStagePreserved(staging, err)
	}
	// Every name in the staged tree is made durable before the rename publishes it: each staged file
	// synced its bytes, but on POSIX not its directory entry, and a power cut after the publish could
	// otherwise leave a restored store missing files the proof above read.
	if err := syncTreeDirs(stagedDot, x.m.barriers); err != nil {
		return proof, maintStagePreserved(staging, err)
	}
	if err := maintPublish(dest, stagedDot, dl.Dot); err != nil {
		return proof, maintStagePreserved(staging, err)
	}
	// And the rename itself, before the restore is reported: without it a power cut could undo the
	// publish, leaving the staging tree and no .qompack for the operator to find.
	if err := x.m.barriers.DirBarrier(dest); err != nil {
		return proof, fmt.Errorf("store: restore backup %q: published %s but could not make it durable: %w",
			id, dl.Dot, err)
	}
	_ = os.Remove(paths.Long(staging)) // best-effort: the staging parent is now empty
	return proof, nil
}

// readValidatedManifest reads and validates the manifest before any file it names is opened: a
// bounded read, a known schema, an id that matches, and every entry's path/size/hash checked,
// including portable case-fold duplicates. The manifest file itself is read no-follow.
func (x *Maintenance) readValidatedManifest(id string) (BackupManifest, error) {
	if err := maintValidateID(id); err != nil {
		return BackupManifest{}, err
	}
	if _, err := os.Lstat(paths.Long(x.pendingCertification(id))); !os.IsNotExist(err) {
		return BackupManifest{}, fmt.Errorf("%w: writer lease certification is pending or unreadable", ErrBackupManifest)
	}
	p := filepath.Join(x.m.backupDir(id), backupManifestFile)
	if err := maintNoFollow(x.m.root, p); err != nil {
		return BackupManifest{}, fmt.Errorf("%w: backup %q: %v", ErrBackupManifest, id, err)
	}
	b, err := maintReadBounded(p, maintMaxManifestBytes)
	if err != nil {
		return BackupManifest{}, fmt.Errorf("%w: backup %q manifest: %v", ErrBackupManifest, id, err)
	}
	var man BackupManifest
	if err := json.Unmarshal(b, &man); err != nil {
		return BackupManifest{}, fmt.Errorf("%w: backup %q: %v", ErrBackupManifest, id, err)
	}
	switch {
	case man.Version != backupManifestVersion:
		return BackupManifest{}, fmt.Errorf("%w: backup %q manifest version %d is not readable by this build",
			ErrBackupManifest, id, man.Version)
	case man.ID != id:
		return BackupManifest{}, fmt.Errorf("%w: backup %q manifest claims id %q", ErrBackupManifest, id, man.ID)
	case !man.Consistent:
		return BackupManifest{}, fmt.Errorf("%w: backup does not claim a consistent snapshot", ErrBackupManifest)
	case len(man.Files) > maintMaxManifestFiles:
		return BackupManifest{}, fmt.Errorf("%w: backup %q lists %d files, over the %d cap",
			ErrBackupManifest, id, len(man.Files), maintMaxManifestFiles)
	}
	exact := make(map[string]bool, len(man.Files))
	fold := make(map[string]string, len(man.Files))
	for _, f := range man.Files {
		if err := maintValidateName(f.Name); err != nil {
			return BackupManifest{}, fmt.Errorf("%w: backup %q: %v", ErrBackupManifest, id, err)
		}
		if f.Size < 0 || f.Size > maintMaxFileBytes {
			return BackupManifest{}, fmt.Errorf("%w: backup %q: %s has size %d out of range",
				ErrBackupManifest, id, f.Name, f.Size)
		}
		if !maintValidSHA(f.SHA256) {
			return BackupManifest{}, fmt.Errorf("%w: backup %q: %s has a malformed digest", ErrBackupManifest, id, f.Name)
		}
		if exact[f.Name] {
			return BackupManifest{}, fmt.Errorf("%w: backup %q names %q more than once", ErrBackupManifest, id, f.Name)
		}
		exact[f.Name] = true
		key := strings.ToLower(f.Name)
		if other, ok := fold[key]; ok {
			return BackupManifest{}, fmt.Errorf("%w: backup %q names %q and %q, which collide case-insensitively",
				ErrBackupManifest, id, other, f.Name)
		}
		fold[key] = f.Name
	}
	return man, nil
}

// verifyTree streams and re-hashes every manifest file where it sits in the backup tree, no-follow
// and under ctx.
func (x *Maintenance) verifyTree(ctx context.Context, id string, man BackupManifest) error {
	tree := filepath.Join(x.m.backupDir(id), backupTreeDir)
	for _, f := range man.Files {
		if err := ctx.Err(); err != nil {
			return err
		}
		src := filepath.Join(tree, filepath.FromSlash(f.Name))
		if err := maintNoFollow(x.m.root, src); err != nil {
			return fmt.Errorf("%w: %s: %v", ErrBackupManifest, f.Name, err)
		}
		if err := maintHashFile(ctx, src, f.Size, f.SHA256); err != nil {
			return fmt.Errorf("%w: %s: %v", ErrBackupCorrupt, f.Name, err)
		}
	}
	return nil
}

// stageRestore writes every manifest file into staging/.qompack, streaming and verifying each backup
// file against the single snapshot. Large (non-protected) files stream through a temp file; the
// bounded §7.4 protected files go through the append-only helpers.
func (x *Maintenance) stageRestore(ctx context.Context, id string, man BackupManifest, staging string) error {
	dl := paths.Of(staging)
	if err := os.MkdirAll(paths.Long(dl.Dot), 0o700); err != nil {
		return fmt.Errorf("store: restore backup %q: layout: %w", id, err)
	}
	tree := filepath.Join(x.m.backupDir(id), backupTreeDir)
	for _, f := range man.Files {
		if err := ctx.Err(); err != nil {
			return err
		}
		src := filepath.Join(tree, filepath.FromSlash(f.Name))
		if err := maintNoFollow(x.m.root, src); err != nil {
			return fmt.Errorf("%w: %s: %v", ErrBackupManifest, f.Name, err)
		}
		out := filepath.Join(dl.Dot, filepath.FromSlash(f.Name))
		if !maintWithin(dl.Dot, out) {
			return fmt.Errorf("%w: %s escapes the destination", ErrBackupManifest, f.Name)
		}
		if err := os.MkdirAll(paths.Long(filepath.Dir(out)), 0o700); err != nil {
			return fmt.Errorf("store: restore backup %q: %w", id, err)
		}

		if err := maintCopyVerify(ctx, staging, src, out, f.Size, f.SHA256); err != nil {
			return fmt.Errorf("store: restore backup %q: %s: %w", id, f.Name, err)
		}
	}
	return nil
}

// verifyStaged re-hashes every manifest file where it now sits in the staged tree, under ctx.
func (x *Maintenance) verifyStaged(ctx context.Context, stagedDot string, man BackupManifest) error {
	for _, f := range man.Files {
		if err := ctx.Err(); err != nil {
			return err
		}
		p := filepath.Join(stagedDot, filepath.FromSlash(f.Name))
		if err := maintHashFile(ctx, p, f.Size, f.SHA256); err != nil {
			return fmt.Errorf("%w: staged %s: %v", ErrBackupCorrupt, f.Name, err)
		}
	}
	return nil
}

// proveReader opens the staged store READ-ONLY (so the proof changes no staged byte) and reads back
// its own roots and tool references. A successful Open alone proves nothing; the payload reads are the
// proof, and a restore that read no content root is refused.
func (x *Maintenance) proveReader(ctx context.Context, staging string, proof *RestoreProof) error {
	rs, err := OpenReadOnly(staging, x.m.cfg, Deps{Clock: x.m.clock, Log: logging.Nop()})
	if err != nil {
		return fmt.Errorf("store: restored backup did not open read-only: %w", err)
	}
	proof.OpenedOK = true
	ro, ok := rs.(readOnlyStore)
	if !ok {
		_ = rs.Close()
		return errors.New("store: restored store is not the built-in read-only store; cannot prove a reader")
	}
	fs := ro.fs

	perr := func() error {
		var audit PublicationAudit
		fs.auditObservationBindings(ctx, &scanBudget{entriesLeft: defaultMaxEntries}, &audit)
		if audit.Incomplete {
			return errors.New("store: restored observation publication is incomplete or unavailable")
		}
		for _, h := range maintRootHashes(fs) {
			if err := ctx.Err(); err != nil {
				return err
			}
			if _, rerr := readRoot(ctx, fs, h); rerr != nil {
				return fmt.Errorf("store: restored store cannot read root %s: %w", h.Short(), rerr)
			}
			proof.ContentRootsProven++
		}
		var tombstoned map[core.Hash]bool // read on the first reference that does not resolve
		for _, ref := range maintToolRoots(fs) {
			if ref.IsZero() {
				continue
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			if _, gerr := fs.GetRoot(ctx, ref); gerr != nil {
				if tombstoned == nil {
					tombstoned = maintTombstonedRoots(fs)
				}
				if errors.Is(gerr, core.ErrNotFound) && tombstoned[ref] {
					// fsck's rule for the same reference (index.tool_use: "accounted for by a gc
					// tombstone"): the store retired this root on purpose.
					proof.ToolRefsTombstoned++
					continue
				}
				return fmt.Errorf("store: restored tool reference root %s does not resolve: %w", ref.Short(), gerr)
			}
			proof.ToolRefsProven++
		}
		return nil
	}()

	if cerr := rs.Close(); cerr != nil && perr == nil {
		perr = fmt.Errorf("store: closing the restored store: %w", cerr)
	}
	if perr != nil {
		return perr
	}
	if proof.ContentRootsProven == 0 {
		return ErrRestoreVacuous
	}
	return nil
}

// ── streaming and path helpers ───────────────────────────────────────────────────────────────

// maintStreamCopyHash reads r under ctx into an optional writer, hashing as it goes and refusing more
// than limit bytes. It returns the hex digest and the byte count.
func maintStreamCopyHash(ctx context.Context, r io.Reader, w io.Writer, limit int64) (string, int64, error) {
	h := sha256.New()
	buf := make([]byte, maintCopyBuf)
	var n int64
	for {
		if err := ctx.Err(); err != nil {
			return "", n, err
		}
		m, rerr := r.Read(buf)
		if m > 0 {
			n += int64(m)
			if n > limit {
				return "", n, errMaintTooLong
			}
			h.Write(buf[:m])
			if w != nil {
				if _, werr := w.Write(buf[:m]); werr != nil {
					return "", n, werr
				}
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return "", n, rerr
		}
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

// maintHashFile streams path and confirms its size and digest match the manifest.
func maintHashFile(ctx context.Context, path string, size int64, sha string) error {
	f, err := os.Open(paths.Long(path))
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	sum, n, err := maintStreamCopyHash(ctx, f, nil, size)
	return maintMatch(sum, n, err, size, sha)
}

// maintCopyVerify exclusively creates each staged file through the append-only
// guard. A failed copy stays in the unpublished staging tree for diagnosis.
func maintCopyVerify(ctx context.Context, root, src, dst string, size int64, sha string) error {
	sf, err := os.Open(paths.Long(src))
	if err != nil {
		return err
	}
	defer func() { _ = sf.Close() }()
	df, err := paths.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	sum, n, copyErr := maintStreamCopyHash(ctx, sf, df, size)
	syncErr := df.Sync()
	closeErr := df.Close()
	if err := errors.Join(maintMatch(sum, n, copyErr, size, sha), syncErr, closeErr); err != nil {
		return err
	}
	// Immutable protected artifacts retain CreateNew's read-only mode. Logs must
	// remain appendable after restore, just as RestoreLog's output does.
	if paths.IsProtected(root, dst) && filepath.Ext(dst) != ".jsonl" {
		return os.Chmod(paths.Long(dst), 0o444)
	}
	return nil
}

// maintMatch turns a stream result into a verify verdict, keeping a size overflow and a context error
// distinct: a cancellation is returned verbatim, a size/hash mismatch becomes ErrBackupCorrupt.
func maintMatch(sum string, n int64, streamErr error, size int64, sha string) error {
	if streamErr != nil {
		if errors.Is(streamErr, errMaintTooLong) {
			return fmt.Errorf("%w: longer than its manifest size", ErrBackupCorrupt)
		}
		return streamErr
	}
	if n != size || sum != sha {
		return fmt.Errorf("%w: size/digest mismatch: %d bytes / %s does not match the manifest", ErrBackupCorrupt, n, sum)
	}
	return nil
}

// maintReadBounded reads at most max bytes from a no-follow-checked path, refusing a larger file.
func maintReadBounded(p string, max int64) ([]byte, error) {
	f, err := os.Open(paths.Long(p))
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	b, err := io.ReadAll(&io.LimitedReader{R: f, N: max + 1})
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > max {
		return nil, fmt.Errorf("larger than the %d-byte limit", max)
	}
	return b, nil
}

// maintRootHashes and maintToolRoots snapshot the restored store's roots and tool-referenced roots
// under its read lock.
func maintRootHashes(s *FSStore) []core.Hash {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]core.Hash, 0, len(s.rootIndex))
	for h := range s.rootIndex {
		out = append(out, h)
	}
	return out
}

// maintTombstonedRoots is every root a gc tombstone in s's index/roots.jsonl retired — the same set
// `qompack fsck` builds for its index.roots and index.tool_use rows, by the same rule: any gc line
// naming the root. The loader drops a retired root from the index without remembering it, so the
// file is read again here; an unreadable file or line answers "not tombstoned", which leaves the
// reference a proof failure rather than excusing it.
func maintTombstonedRoots(s *FSStore) map[core.Hash]bool {
	out := map[core.Hash]bool{}
	f, err := os.Open(paths.Long(filepath.Join(s.l.Index, rootsFile)))
	if err != nil {
		return out
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, scannerInitialBuf), scannerMaxBuf)
	for sc.Scan() {
		if _, tombstone, root, perr := parseRootLine(sc.Bytes()); perr == nil && tombstone {
			out[root] = true
		}
	}
	return out
}

func maintToolRoots(s *FSStore) []core.Hash {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]core.Hash, 0, len(s.toolUse))
	for _, rec := range s.toolUse {
		out = append(out, rec.Root)
	}
	return out
}

// maintRefuseExistingDot refuses when dot exists as anything — dir, file, symlink or reparse alias —
// using Lstat so an alias is detected rather than followed.
func maintRefuseExistingDot(dot string) error {
	fi, err := os.Lstat(paths.Long(dot))
	switch {
	case err == nil:
		kind := "a directory"
		switch {
		case fi.Mode()&os.ModeSymlink != 0:
			kind = "a symlink"
		case fi.Mode()&os.ModeType != 0 && !fi.IsDir():
			kind = "a special file or reparse alias"
		case !fi.IsDir():
			kind = "a file"
		}
		return fmt.Errorf("%w: %s is %s", ErrRestoreTargetExists, dot, kind)
	case os.IsNotExist(err):
		return nil
	default:
		return fmt.Errorf("store: checking restore destination %s: %w", dot, err)
	}
}

// maintPublish uses a platform primitive that atomically refuses any existing
// destination. It leaves no lockfile that could wedge a retry after a crash.
func maintPublish(_ string, stagedDot, destDot string) error {
	if err := maintRefuseExistingDot(destDot); err != nil {
		return err
	}
	return paths.RenameDirectoryNoReplace(stagedDot, destDot)
}

// maintStagingRoot creates a per-restore staging directory inside dest, so the staged .qompack and
// the final .qompack share a filesystem.
func maintStagingRoot(dest, id string) (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	staging := filepath.Join(dest, ".qompack.restore-"+id+"-"+hex.EncodeToString(b[:]))
	if err := os.MkdirAll(paths.Long(staging), 0o700); err != nil {
		return "", err
	}
	return staging, nil
}

// maintStagePreserved wraps a restore failure with the staging path, which is deliberately not
// removed so a failed restore is inspectable evidence.
func maintStagePreserved(staging string, cause error) error {
	return fmt.Errorf("%w; the incomplete staging tree is preserved as evidence at %s", cause, staging)
}

// maintNoFollow refuses when any path component between anchor and full is a symlink or reparse
// point. anchor is a trusted root (the project root); its own ancestors are not inspected, since a
// legitimate temp root can itself sit under a symlinked ancestor.
//
// Both arguments are in the spelling this package keeps every path in, never paths.Long's \\?\
// form: that is applied here, per component, at the Lstat. filepath.Rel cannot relate a prefixed
// path to an unprefixed anchor, so a prefixed full is refused with Rel's error, and TakeBackup's
// walk once did exactly that to every project past MAX_PATH (C1.7).
func maintNoFollow(anchor, full string) error {
	rel, err := filepath.Rel(anchor, full)
	if err != nil {
		return err
	}
	if rel == "." {
		return nil
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return fmt.Errorf("%s is outside %s", full, anchor)
	}
	cur := anchor
	for _, seg := range strings.Split(rel, string(filepath.Separator)) {
		if seg == "" || seg == "." {
			continue
		}
		cur = filepath.Join(cur, seg)
		fi, lerr := os.Lstat(paths.Long(cur))
		if lerr != nil {
			return lerr
		}
		if fi.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0 {
			return fmt.Errorf("path component %s is a symlink or reparse point", cur)
		}
	}
	return nil
}

// maintWithin reports whether target resolves inside base by path arithmetic (defense in depth).
func maintWithin(base, target string) bool {
	rel, err := filepath.Rel(base, target)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// maintValidSHA reports whether s is a 64-character lowercase hex digest, the shape TakeBackup writes.
func maintValidSHA(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, r := range s {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')) {
			return false
		}
	}
	return true
}

// maintValidateID accepts only a bounded [A-Za-z0-9._-] name that is not a dot form, does not start
// with a dot, and is not a reserved device name.
func maintValidateID(id string) error {
	switch {
	case id == "":
		return fmt.Errorf("%w: empty", ErrBackupID)
	case len(id) > 128:
		return fmt.Errorf("%w: %q is longer than 128 bytes", ErrBackupID, id)
	case id == "." || id == "..":
		return fmt.Errorf("%w: %q", ErrBackupID, id)
	case strings.HasPrefix(id, "."):
		return fmt.Errorf("%w: %q may not start with a dot", ErrBackupID, id)
	}
	for _, r := range id {
		if r == '-' || r == '_' || r == '.' ||
			(r >= '0' && r <= '9') || (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') {
			continue
		}
		return fmt.Errorf("%w: %q contains %q; use only [A-Za-z0-9._-]", ErrBackupID, id, string(r))
	}
	if maintReservedComponent(id) {
		return fmt.Errorf("%w: %q is a reserved device name", ErrBackupID, id)
	}
	return nil
}

// maintValidateName accepts only a clean, slash-relative manifest name whose every component is
// portable: no NUL/backslash, not absolute, no ".." or ".", no ":" (drive letter or NTFS ADS), no
// reserved device name, no trailing dot or space, and no control character.
func maintValidateName(name string) error {
	switch {
	case name == "":
		return errors.New("a manifest entry has an empty name")
	case strings.ContainsRune(name, 0):
		return fmt.Errorf("name %q contains a NUL", name)
	case strings.ContainsRune(name, '\\'):
		return fmt.Errorf("name %q contains a backslash; manifest names are slash-relative", name)
	case strings.HasPrefix(name, "/") || path.IsAbs(name):
		return fmt.Errorf("name %q is absolute", name)
	case path.Clean(name) != name:
		return fmt.Errorf("name %q is not in clean relative form", name)
	}
	for _, seg := range strings.Split(name, "/") {
		if err := maintValidateComponent(seg); err != nil {
			return fmt.Errorf("name %q: %w", name, err)
		}
	}
	return nil
}

// maintValidateComponent checks one path segment.
func maintValidateComponent(seg string) error {
	switch {
	case seg == "":
		return errors.New("has an empty segment")
	case seg == "." || seg == "..":
		return fmt.Errorf("segment %q traverses", seg)
	case strings.ContainsRune(seg, ':'):
		return fmt.Errorf("segment %q contains ':' (drive or NTFS stream)", seg)
	case strings.HasSuffix(seg, ".") || strings.HasSuffix(seg, " "):
		return fmt.Errorf("segment %q ends with a dot or space", seg)
	case maintReservedComponent(seg):
		return fmt.Errorf("segment %q is a reserved device name", seg)
	}
	for _, r := range seg {
		if r < 0x20 {
			return fmt.Errorf("segment %q has a control character", seg)
		}
	}
	return nil
}

// maintReservedComponent reports whether seg (or its stem before the first dot) is a Windows reserved
// device name, which cannot be a directory even on other platforms without surprising an operator.
func maintReservedComponent(seg string) bool {
	stem := seg
	if i := strings.IndexByte(stem, '.'); i >= 0 {
		stem = stem[:i]
	}
	up := strings.ToUpper(stem)
	switch up {
	case "CON", "PRN", "AUX", "NUL":
		return true
	}
	if len(up) == 4 && (strings.HasPrefix(up, "COM") || strings.HasPrefix(up, "LPT")) {
		return up[3] >= '1' && up[3] <= '9'
	}
	return false
}

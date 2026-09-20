package store

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
)

// Consistent backup, verified restore, and the rehearsed rollback drill (SP-20 M1-04 /
// T20-M1-08).
//
// ADR 0013 leaves this open in as many words: the delivery-lease pair "does not detect wholesale
// rollback/loss of both files; consistent backup and outer migration authority remain required."
// This file is that outer authority for the migration itself. It is deliberately executable
// rather than documented: the plan's requirement is a rollback DRILL run before and after the
// first new-format write, and a drill nobody can run is a paragraph.
//
// What the drill does NOT do is promise an automatic downgrade. RollbackDrill.AutomaticDowngrade
// is false in every outcome this file can produce. Rolling a migration back means stopping the
// incompatible writers, restoring a backup that has been verified byte for byte, and knowing
// exactly which writes the older binary cannot read — not silently rewriting new artifacts into
// an old shape, which would lose evidence to make a report look clean.

// ErrBackupCorrupt means a backed-up file no longer matches its manifest entry.
var ErrBackupCorrupt = errors.New("store: backup does not match its manifest")

// ErrBackupMoved means a writer this process does not hold the lease of changed the project's
// delivery state while the copy was running, so what was captured is a copy of a moving target
// rather than a backup. The writer is internal/daemon: see backupLiveWriterFiles.
var ErrBackupMoved = errors.New("store: the project changed while the backup ran")

const (
	// backupManifestVersion is the version of a backup's manifest.json.
	backupManifestVersion = 1
	// backupTreeDir is the subdirectory holding the copied .qompack tree.
	backupTreeDir = "tree"
	// backupManifestFile is the manifest's filename inside a backup directory.
	backupManifestFile = "manifest.json"
)

// backupSkipDirs are the .qompack subdirectories a backup deliberately omits: volatile runtime
// state that would make two backups of identical data differ, plus the backup tree itself, which
// would otherwise nest without bound.
var backupSkipDirs = map[string]bool{
	"backup": true, "run": true, "tmp": true, "logs": true, "metrics": true,
}

// The delivery-seal sidecars internal/daemon keeps beside its two journals under state/. They are
// named here as strings for the same reason lifecycle.go names the journals that way: daemon
// imports store, so store cannot import daemon back (00-ARCHITECTURE.md §3.2).
const (
	// deliveryLeasePositionFile is the lease journal's sealed position. Since SP20-D1 step 2 a
	// running daemon rewrites it IN PLACE — one 480-byte WriteAt into a held 32 KiB A/B image,
	// then a datasync — instead of replacing it by rename, so a plain os.ReadFile issued while the
	// daemon is sealing can capture a slot that is neither the old record nor the new one. The
	// daemon's reader refuses such an image outright rather than falling back to the other slot,
	// which would make the restored project's delivery journal unavailable. That is design risk
	// R10, and refuseIfTheProjectMoved below is what keeps it out of a backup that claims to be
	// consistent.
	deliveryLeasePositionFile = "delivery-lease-position.json"
	// deliveryAckPositionFile is the acknowledgement journal's sealed position, sealed the same way.
	deliveryAckPositionFile = "delivery-ack-position.json"
)

// backupLiveWriterFiles are the DELIVERY-state files a running daemon can change under the walk, as
// slash-relative names under .qompack. The two journals are here as well as the two sidecars: they
// are append-only, so a copy of one can hold a torn tail, and a journal captured before a sidecar
// that seals more bytes than the copy holds is the "inconsistent live pair" the same risk row names.
//
// It is NOT every file with a writer outside the single writer lease, and the guard does not claim
// to be. internal/daemon takes no store lease at all, and the walk also copies index/tool_use.jsonl
// (RecordToolUse), spool/wal-*.ndjson and spool/client-*.ndjson (the latter written by the hook
// client, a third process), records/captures/** and objects/** (WriteCaptureSidecar), and
// state/drain.json. Those tails are tolerated for the reason the store already tolerates them
// everywhere else: each is append-only or replaced whole, so the worst a mid-copy read captures is a
// short prefix, and a reader of the restored tree discards an incomplete trailing line rather than
// failing. The delivery seals are the exception this list exists for — since SP20-D1 step 2 they are
// rewritten IN PLACE, so a mid-write copy is neither the old record nor the new one, and the
// daemon's own reader refuses such an image OUTRIGHT rather than reading a prefix of it, which takes
// the restored project's delivery journal with it (risk R10).
var backupLiveWriterFiles = []string{
	"state/" + deliveryLeaseFile,
	"state/" + deliveryAckFile,
	"state/" + deliveryLeasePositionFile,
	"state/" + deliveryAckPositionFile,
}

// BackupWatchedFiles is backupLiveWriterFiles, copied, as slash-relative names under .qompack.
//
// It is exported for one caller and one purpose: internal/daemon owns these four filenames as
// constants, store must name them as string literals (daemon imports store, so store cannot import
// daemon back — 00-ARCHITECTURE.md §3.2), and nothing otherwise holds the two spellings together.
// The journals at least have a functional cross-check, since store's own GC reads them; the two
// seal sidecars have none, and store never opens them for any other purpose. So a daemon-side
// rename would take refuseIfTheProjectMoved's `!copied && os.IsNotExist → continue` branch for both,
// turn the R10 guard into a silent no-op for the very files it exists for, and leave every test
// passing — this package's own included, since its fixtures write the literals themselves.
//
// TestBackupWatchedFiles_NamesTheDeliveryStateThisPackageWrites asserts the containment from the
// side that has the constants. The copy is deliberate: a caller must not be able to shorten the
// list it is checking itself against.
func BackupWatchedFiles() []string {
	return append([]string(nil), backupLiveWriterFiles...)
}

// BackupFile is one file inside a backup: its slash-relative name under .qompack, its size, and
// its content digest. Verification re-hashes; nothing is trusted because it is merely present.
type BackupFile struct {
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// BackupManifest is a backup's manifest.json.
type BackupManifest struct {
	Version int            `json:"version"`
	ID      string         `json:"id"`
	Root    string         `json:"root"`
	TakenAt core.UnixMilli `json:"taken_at"`
	// Consistent records that the backup was taken while this process held the single writer
	// lease, after the store had been flushed, and with the project's DELIVERY state
	// (backupLiveWriterFiles) unchanged from the first byte copied to the last — which is what makes
	// it a consistent backup rather than a copy of a moving target.
	//
	// The third condition is not redundant. The writer lease excludes other STORE writers only:
	// internal/daemon takes no store lease and, since SP20-D1 step 2, rewrites its delivery-seal
	// sidecars in place while it serves. TakeBackup returns ErrBackupMoved rather than writing a
	// manifest when that condition fails, so this field is never true of a copy the daemon moved
	// under.
	//
	// It is scoped to the delivery state deliberately, and the scope is the honest one: the daemon
	// writes several other copied files with no store lease either (backupLiveWriterFiles names
	// them), and a mid-copy capture of those is a short append-only tail a reader steps over, not a
	// file its reader refuses. This field does not promise they were quiet. A backup taken with the
	// daemon stopped is the only thing that does, which is what the ErrBackupMoved advice says.
	Consistent bool `json:"consistent"`
	// SnapshotID and Frontier record where the migration stood when the backup was taken, so a
	// restore can be reasoned about against the import.
	SnapshotID string       `json:"snapshot_id"`
	Frontier   int64        `json:"frontier"`
	Files      []BackupFile `json:"files"`
}

func (m *Migrator) backupDir(id string) string { return filepath.Join(m.l.Backup, id) }

// TakeBackup makes a consistent backup of the project's .qompack tree under id.
//
// Consistency comes from four things, in this order: the single writer lease is held for the
// whole copy, so no other STORE writer is running; the store is flushed first, so buffered writes
// are on disk before anything is read; every copied byte is hashed as it is written, so the
// manifest describes what was actually captured rather than what was intended; and the project's
// delivery state (backupLiveWriterFiles) is re-read at the end and must still hold exactly what was
// captured (refuseIfTheProjectMoved), so a daemon that sealed a delivery under the walk fails the
// backup instead of being recorded as consistent. That last condition covers the delivery state and
// says so: the other files a daemon writes without a store lease are append-only tails a reader
// steps over, and backupLiveWriterFiles names them and why.
//
// A backup id is claimed exactly once. A second TakeBackup under the same id is os.ErrExist, not
// an overwrite: a backup is evidence, and silently replacing evidence is the failure mode the
// rollback requirement exists to prevent. A failed backup claims its id the same way — the tree
// it copied stays on disk with no manifest beside it, which is what every other mid-walk failure
// already leaves and what makes an unfinished backup unusable as one (VerifyBackup needs the
// manifest) rather than silently retryable over.
//
// ErrBackupMoved is the one exception, and how an operator retries follows from it. That refusal
// is the expected answer beside a running daemon, not an exceptional one, so it removes the
// incomplete backup/<id> before returning and the same id is free again: stop the daemon and rerun
// the identical command. Any other failure keeps its id, so a retry needs a new one — and the
// directory the failed attempt left is safe to delete by hand, since a backup without a manifest
// can never verify or restore.
func (m *Migrator) TakeBackup(ctx context.Context, id string) (BackupManifest, error) {
	if id == "" {
		return BackupManifest{}, errors.New("store: TakeBackup needs a backup id")
	}
	dir := m.backupDir(id)
	if _, err := os.Stat(paths.Long(dir)); err == nil {
		return BackupManifest{}, fmt.Errorf("store: backup %q: %w", id, os.ErrExist)
	} else if !os.IsNotExist(err) {
		return BackupManifest{}, fmt.Errorf("store: backup %q: %w", id, err)
	}

	h, err := m.Handoff()
	if err != nil {
		return BackupManifest{}, err
	}
	lease, err := m.AcquireWriter(h.Owner)
	if err != nil {
		return BackupManifest{}, fmt.Errorf("store: backup %q needs the writer quiesced: %w", id, err)
	}
	defer func() { _ = lease.Release() }()

	if err := m.s.Flush(ctx); err != nil {
		return BackupManifest{}, fmt.Errorf("store: backup %q: flush: %w", id, err)
	}

	cur, err := m.Cursor()
	if err != nil {
		return BackupManifest{}, err
	}
	man := BackupManifest{
		Version: backupManifestVersion, ID: id, Root: m.root, TakenAt: m.now(),
		Consistent: true, SnapshotID: cur.SnapshotID, Frontier: cur.Position,
	}

	tree := filepath.Join(dir, backupTreeDir)
	err = filepath.WalkDir(paths.Long(m.l.Dot), func(p string, d fs.DirEntry, werr error) error {
		if werr != nil {
			return werr
		}
		rel, rerr := filepath.Rel(paths.Long(m.l.Dot), p)
		if rerr != nil {
			return rerr
		}
		rel = filepath.ToSlash(rel)
		if rel == "." {
			return nil
		}
		if d.IsDir() {
			if backupSkipDirs[strings.SplitN(rel, "/", 2)[0]] {
				return filepath.SkipDir
			}
			return nil
		}
		if backupSkipDirs[strings.SplitN(rel, "/", 2)[0]] || rel == "migrate/"+writerLockFile {
			return nil
		}
		// paths.ReadFileShared, not os.ReadFile, for every copied file. On Windows an ordinary read
		// handle carries no FILE_SHARE_DELETE, so while it is open the daemon's own
		// paths.WriteAtomic of that path fails — the R10 hazard running the other way, the backup
		// stalling the daemon. test/guards' sharedReaders row for this function is the inventory
		// that stops the answer reverting.
		b, ferr := paths.ReadFileShared(p)
		if ferr != nil {
			return ferr
		}
		dst := filepath.Join(tree, filepath.FromSlash(rel))
		if merr := os.MkdirAll(paths.Long(filepath.Dir(dst)), 0o700); merr != nil {
			return merr
		}
		if werr := paths.WriteAtomic(dst, b, 0o600); werr != nil {
			return werr
		}
		sum := sha256.Sum256(b)
		man.Files = append(man.Files, BackupFile{Name: rel, Size: int64(len(b)), SHA256: hex.EncodeToString(sum[:])})
		return nil
	})
	if err != nil {
		return BackupManifest{}, fmt.Errorf("store: backup %q: %w", id, err)
	}
	sort.Slice(man.Files, func(i, j int) bool { return man.Files[i].Name < man.Files[j].Name })

	if m.afterBackupWalk != nil {
		m.afterBackupWalk()
	}
	if err := m.refuseIfTheProjectMoved(man); err != nil {
		// This refusal, and only this one, releases the id with the copy. It is the one failure
		// that is EXPECTED beside a live daemon rather than exceptional — any delivery or
		// acknowledgement batch during the walk produces it — so leaving a full junk tree behind
		// and burning the name the operator asked for would turn "stop the daemon and try again"
		// into "stop the daemon and invent a new id". What is removed provably has no manifest:
		// the manifest is written further down, and a second TakeBackup under this id could not
		// have reached the walk at all (the Stat above is os.ErrExist).
		if rmErr := os.RemoveAll(paths.Long(dir)); rmErr != nil {
			return BackupManifest{}, fmt.Errorf(
				"store: backup %q: %w; its incomplete tree could not be removed (%v), so retry under "+
					"another id or remove that directory first", id, err, rmErr)
		}
		return BackupManifest{}, fmt.Errorf("store: backup %q: %w", id, err)
	}

	// A backup is rollback material, and a rollback drill re-reads every imported object out of
	// the LIVE store to compare it against the restored one. So the manifest's own frontier is
	// declared retained: an import performed by a build that predates this declaration, or a
	// mapping log restored from elsewhere, is covered by the backup that names it.
	if err := m.retainFrontier("restorable through backup " + id + "/" + backupManifestFile); err != nil {
		return BackupManifest{}, fmt.Errorf("store: backup %q: %w", id, err)
	}

	b, err := json.Marshal(man)
	if err != nil {
		return BackupManifest{}, fmt.Errorf("store: encode backup manifest: %w", err)
	}
	if err := paths.WriteAtomic(filepath.Join(dir, backupManifestFile), b, 0o600); err != nil {
		return BackupManifest{}, err
	}
	return man, nil
}

// refuseIfTheProjectMoved re-reads every backupLiveWriterFiles entry after the copy walk and
// reports ErrBackupMoved unless the live file still holds exactly what the walk captured: the
// same size and the same digest for a file the manifest names, and still no file at all for one
// it does not.
//
// It is a comparison against the CAPTURED bytes, not against a second live read, because the
// hazard is a reader that is not atomic against a concurrent writer: a read of a 32 KiB seal can
// return a mixture of the bytes before and after a 480-byte WriteAt, and that mixture equals
// neither the file the walk started from nor the file on disk now. A seal write always changes
// the slot's record, so any write under the walk is a difference here.
//
// The read itself goes through paths.ReadFileShared for the reason every other reader of a file a
// daemon replaces does: an os.ReadFile handle grants no FILE_SHARE_DELETE on Windows, so for as
// long as it is open the daemon's paths.WriteAtomic of that same sidecar fails. This function
// exists to stop the daemon damaging the backup; reading it the ordinary way would have the backup
// damage the daemon instead — openSealHandle's conversion faults the journal for that daemon's
// whole life, and closeSeals' downgrade silently leaves v2 behind.
//
// It therefore also refuses a copy that is whole but stale — the daemon sealed a batch after this
// file was read and before the walk ended — and that is deliberate: the manifest's Consistent
// claim is about the whole tree, and a tree copied around a live writer does not support it.
func (m *Migrator) refuseIfTheProjectMoved(man BackupManifest) error {
	captured := make(map[string]BackupFile, len(man.Files))
	for _, f := range man.Files {
		captured[f.Name] = f
	}
	for _, name := range backupLiveWriterFiles {
		live, rerr := paths.ReadFileShared(filepath.Join(m.l.Dot, filepath.FromSlash(name)))
		f, copied := captured[name]
		switch {
		case !copied && os.IsNotExist(rerr):
			continue
		case !copied:
			return fmt.Errorf("%w: %s appeared while the backup ran", ErrBackupMoved, name)
		case rerr != nil:
			return fmt.Errorf("%w: %s: %v", ErrBackupMoved, name, rerr)
		}
		sum := sha256.Sum256(live)
		if int64(len(live)) != f.Size || hex.EncodeToString(sum[:]) != f.SHA256 {
			return fmt.Errorf("%w: %s changed under the copy; take the backup with the daemon stopped", ErrBackupMoved, name)
		}
	}
	return nil
}

// VerifyBackup re-hashes every file the manifest names and reports the manifest only if all of
// them still match. A file that is missing, has drifted in size, or hashes differently makes the
// whole backup ErrBackupCorrupt — a partially valid backup is not a rollback path.
func (m *Migrator) VerifyBackup(id string) (BackupManifest, error) {
	if id == "" {
		return BackupManifest{}, errors.New("store: VerifyBackup needs a backup id")
	}
	b, err := os.ReadFile(paths.Long(filepath.Join(m.backupDir(id), backupManifestFile)))
	if err != nil {
		return BackupManifest{}, fmt.Errorf("store: backup %q manifest: %w", id, err)
	}
	var man BackupManifest
	if err := json.Unmarshal(b, &man); err != nil {
		return BackupManifest{}, fmt.Errorf("store: parse backup %q manifest: %w", id, err)
	}
	if man.Version != backupManifestVersion {
		return BackupManifest{}, fmt.Errorf("store: backup %q manifest version %d is not readable by this build",
			id, man.Version)
	}
	tree := filepath.Join(m.backupDir(id), backupTreeDir)
	for _, f := range man.Files {
		got, rerr := os.ReadFile(paths.Long(filepath.Join(tree, filepath.FromSlash(f.Name))))
		if rerr != nil {
			return BackupManifest{}, fmt.Errorf("%w: %s: %v", ErrBackupCorrupt, f.Name, rerr)
		}
		if int64(len(got)) != f.Size {
			return BackupManifest{}, fmt.Errorf("%w: %s: %d bytes, manifest says %d",
				ErrBackupCorrupt, f.Name, len(got), f.Size)
		}
		sum := sha256.Sum256(got)
		if hex.EncodeToString(sum[:]) != f.SHA256 {
			return BackupManifest{}, fmt.Errorf("%w: %s: digest mismatch", ErrBackupCorrupt, f.Name)
		}
	}
	return man, nil
}

// RestoreBackup verifies the backup and writes it out as a project tree under dest, which is a
// PROJECT root: dest/.qompack is what gets populated. The result is openable by an ordinary
// store.Open, which is the point — a backup that cannot be opened is not a verified restore.
//
// Files under §7.4's protected paths split by kind. A *.jsonl (checkpoints/MANIFEST.jsonl,
// pins/invariants.jsonl) is an append-only log: RestoreLog creates it if absent (O_APPEND,
// 0600, Sync) so a later seal or pin can append. Artifacts (checkpoints/NNNN.json) and
// sketches/tried.bloom keep CreateNew — 0444 is right for them. A restore never overwrites:
// both writers return os.ErrExist and this function wraps it naming the file.
func (m *Migrator) RestoreBackup(id, dest string) error {
	man, err := m.VerifyBackup(id)
	if err != nil {
		return err
	}
	dl := paths.Of(dest)
	if err := paths.EnsureLayout(dl); err != nil {
		return fmt.Errorf("store: restore backup %q: %w", id, err)
	}
	tree := filepath.Join(m.backupDir(id), backupTreeDir)
	for _, f := range man.Files {
		b, rerr := os.ReadFile(paths.Long(filepath.Join(tree, filepath.FromSlash(f.Name))))
		if rerr != nil {
			return fmt.Errorf("%w: %s: %v", ErrBackupCorrupt, f.Name, rerr)
		}
		out := filepath.Join(dl.Dot, filepath.FromSlash(f.Name))
		if err := os.MkdirAll(paths.Long(filepath.Dir(out)), 0o700); err != nil {
			return fmt.Errorf("store: restore backup %q: %w", id, err)
		}
		if paths.IsProtected(dest, out) {
			var werr error
			if filepath.Ext(out) == ".jsonl" {
				werr = paths.RestoreLog(out, b)
			} else {
				werr = paths.CreateNew(out, b)
			}
			if werr != nil {
				return fmt.Errorf("store: restore backup %q: %s: %w", id, f.Name, werr)
			}
			continue
		}
		if err := paths.WriteAtomic(out, b, 0o600); err != nil {
			return fmt.Errorf("store: restore backup %q: %w", id, err)
		}
	}
	return nil
}

// ── the rollback drill ─────────────────────────────────────────────────────────────────────

// RollbackPhase is when a drill is being run relative to the first new-format write. The plan
// requires both, and they are genuinely different rehearsals: before, the rollback target is a
// store the old binary already understands; after, there is at least one artifact it does not.
type RollbackPhase string

const (
	// RollbackBeforeFirstNewWrite rehearses the rollback while no new-format write exists yet.
	RollbackBeforeFirstNewWrite RollbackPhase = "before-first-new-format-write"
	// RollbackAfterFirstNewWrite rehearses it again against the actual new artifacts.
	RollbackAfterFirstNewWrite RollbackPhase = "after-first-new-format-write"
)

// RollbackOptions configures one rehearsal.
type RollbackOptions struct {
	Phase RollbackPhase
	// BackupID names the backup the rollback would restore.
	BackupID string
	// RestoreRoot is a scratch project root the backup is restored into and opened from. It must
	// not be the live project: the drill proves a restore works, it does not perform one.
	RestoreRoot string
	// StopWriters stops every writer that would be incompatible after a rollback. It is required.
	StopWriters func(context.Context) error
}

// RollbackDrill is one rehearsal's durable record, appended to migrate/rollback.jsonl.
type RollbackDrill struct {
	Version  int            `json:"version"`
	Phase    RollbackPhase  `json:"phase"`
	At       core.UnixMilli `json:"at"`
	BackupID string         `json:"backup_id"`
	// WritersStopped, BackupVerified and ReaderProved are the three things the drill actually
	// demonstrates, each recorded separately so a partial rehearsal cannot read as a whole one.
	WritersStopped bool   `json:"writers_stopped"`
	BackupVerified bool   `json:"backup_verified"`
	ReaderProved   bool   `json:"reader_proved"`
	RestoreRoot    string `json:"restore_root"`
	// RetainedLegacyIDs and RetainedRoots are the identities that survived the rehearsal. Old ids
	// keep resolving through a rollback or the drill has failed.
	RetainedLegacyIDs []string `json:"retained_legacy_ids"`
	RetainedRoots     []string `json:"retained_roots"`
	// UnreadableByOldReader is the explicit handling of "writes an older binary cannot read": the
	// new-format writes made after the backup was taken, enumerated rather than hand-waved.
	UnreadableByOldReader []NewFormatWrite `json:"unreadable_by_old_reader"`
	// AutomaticDowngrade is always false. It is a field rather than a comment so the record on
	// disk states, every time, that no automatic downgrade was performed or promised.
	AutomaticDowngrade bool `json:"automatic_downgrade"`
	// EvidenceRetained records that nothing the drill did removed an object from the live store.
	EvidenceRetained bool `json:"evidence_retained"`
	OK               bool `json:"ok"`
	// Refusal is why the drill did not pass, empty when OK.
	Refusal string `json:"refusal,omitempty"`
}

// RehearseRollback runs the drill and records it. A drill that cannot pass is a RESULT, not an
// error: "the backup does not verify" is exactly the finding a rehearsal exists to produce, and
// swallowing it into an error would lose the rest of the record. Only genuine I/O failures —
// being unable to read the mapping log, being unable to append the drill record — are errors.
//
// The steps, in the order they run:
//
//  1. Stop the incompatible writers, then take the single writer lease and hold it for the whole
//     drill, so nothing writes underneath the rehearsal.
//  2. Verify the named backup by re-hashing every file in it.
//  3. Check the claimed phase against the new-format write log, and enumerate the writes made
//     after the backup was taken — the ones an older binary cannot read.
//  4. Restore the backup into a scratch root, open a real Store over it, and re-read every
//     imported object through its OLD legacy identity, comparing bytes against the live store.
//  5. Confirm nothing was deleted: every mapped root and every new-format artifact is still in
//     the live store afterwards.
func (m *Migrator) RehearseRollback(ctx context.Context, o RollbackOptions) (RollbackDrill, error) {
	d := RollbackDrill{
		Version: rollbackDrillVersion, Phase: o.Phase, At: m.now(), BackupID: o.BackupID,
		RestoreRoot: o.RestoreRoot, AutomaticDowngrade: false,
	}
	if o.Phase != RollbackBeforeFirstNewWrite && o.Phase != RollbackAfterFirstNewWrite {
		return d, fmt.Errorf("store: rollback drill: unknown phase %q", o.Phase)
	}
	if o.RestoreRoot == "" {
		return d, errors.New("store: rollback drill needs a scratch restore root")
	}

	// 1. stop the incompatible writers and hold the lease.
	if o.StopWriters == nil {
		return m.recordDrill(d, "no way to stop the incompatible writers was supplied")
	}
	if err := o.StopWriters(ctx); err != nil {
		return m.recordDrill(d, fmt.Sprintf("the incompatible writers could not be stopped: %v", err))
	}
	h, err := m.Handoff()
	if err != nil {
		return d, err
	}
	lease, err := m.AcquireWriter(h.Owner)
	if err != nil {
		return m.recordDrill(d, fmt.Sprintf("the writer lease could not be taken: %v", err))
	}
	defer func() { _ = lease.Release() }()
	d.WritersStopped = true

	// 2. verify the backup.
	man, err := m.VerifyBackup(o.BackupID)
	if err != nil {
		return m.recordDrill(d, fmt.Sprintf("the backup did not verify: %v", err))
	}
	d.BackupVerified = true

	// 3. phase check and the writes an older binary cannot read.
	writes, err := m.NewFormatWrites()
	if err != nil {
		return d, err
	}
	for _, w := range writes {
		if w.At >= man.TakenAt {
			d.UnreadableByOldReader = append(d.UnreadableByOldReader, w)
		}
	}
	switch o.Phase {
	case RollbackBeforeFirstNewWrite:
		if len(writes) > 0 {
			return m.recordDrill(d, fmt.Sprintf(
				"the before phase claims no new-format write exists, but %d are recorded", len(writes)))
		}
	case RollbackAfterFirstNewWrite:
		if len(d.UnreadableByOldReader) == 0 {
			return m.recordDrill(d,
				"the after phase must rehearse against a real new artifact, but none was written after the backup")
		}
	}

	// 4. restore and prove a reader.
	if err := m.RestoreBackup(o.BackupID, o.RestoreRoot); err != nil {
		return m.recordDrill(d, fmt.Sprintf("the backup did not restore: %v", err))
	}
	_, order, err := m.Frontier()
	if err != nil {
		return d, err
	}
	rs, err := Open(o.RestoreRoot, m.cfg, Deps{Clock: m.clock})
	if err != nil {
		return m.recordDrill(d, fmt.Sprintf("the restored backup did not open as a store: %v", err))
	}
	defer func() { _ = rs.Close() }()

	for _, mp := range order {
		live, lerr := readRoot(ctx, m.s, mp.Root)
		if lerr != nil {
			return m.recordDrill(d, fmt.Sprintf("live store lost %s (%s): %v", mp.LegacyID, mp.Root.Short(), lerr))
		}
		got, rerr := readRoot(ctx, rs, mp.Root)
		if rerr != nil {
			return m.recordDrill(d, fmt.Sprintf("the restored store cannot read old id %s (%s): %v",
				mp.LegacyID, mp.Root.Short(), rerr))
		}
		if !bytes.Equal(live, got) {
			return m.recordDrill(d, fmt.Sprintf("the restored store served different bytes for old id %s", mp.LegacyID))
		}
		d.RetainedLegacyIDs = append(d.RetainedLegacyIDs, mp.LegacyID)
		d.RetainedRoots = append(d.RetainedRoots, mp.Root.String())
	}
	d.ReaderProved = true

	// 5. evidence retention: the drill deletes nothing, and says so against the live store.
	d.EvidenceRetained = true
	for _, mp := range order {
		if _, gerr := m.s.GetRoot(ctx, mp.Root); gerr != nil {
			d.EvidenceRetained = false
		}
	}
	for _, w := range writes {
		nh, perr := core.ParseHash(w.Root)
		if perr != nil {
			d.EvidenceRetained = false
			continue
		}
		if _, gerr := m.s.GetRoot(ctx, nh); gerr != nil {
			d.EvidenceRetained = false
		}
	}
	if !d.EvidenceRetained {
		return m.recordDrill(d, "evidence was lost: an object present before the drill is gone")
	}

	d.OK = true
	return m.recordDrill(d, "")
}

// retainFrontier declares every root the mapping log currently names as a retention root.
//
// It reads the log rather than a caller's list so that it covers whatever is actually on disk,
// including an import a build without this declaration performed.
func (m *Migrator) retainFrontier(reason string) error {
	_, order, err := m.Frontier()
	if err != nil {
		return err
	}
	hs := make([]core.Hash, 0, len(order))
	for _, mp := range order {
		hs = append(hs, mp.Root)
	}
	return m.retainRoots(reason, hs...)
}

// recordDrill appends the drill record and returns it. The log is append-only, so a later
// rehearsal can never overwrite an earlier one's finding.
//
// Every root the record NAMES is declared a retention root first. A rollback record whose objects
// GC has since collected is not a rollback path, it is a claim about one — and the record names
// two kinds: the roots the drill proved re-readable through their old ids, and the new-format
// writes it enumerated as unreadable by an older binary. Both must outlive the pass.
func (m *Migrator) recordDrill(d RollbackDrill, refusal string) (RollbackDrill, error) {
	d.Refusal = refusal
	d.OK = refusal == ""
	hs := make([]core.Hash, 0, len(d.RetainedRoots)+len(d.UnreadableByOldReader))
	for _, s := range d.RetainedRoots {
		if h, err := core.ParseHash(s); err == nil {
			hs = append(hs, h)
		}
	}
	for _, w := range d.UnreadableByOldReader {
		if h, err := core.ParseHash(w.Root); err == nil {
			hs = append(hs, h)
		}
	}
	if err := m.retainRoots("named by migrate/"+rollbackDrillFile, hs...); err != nil {
		return d, fmt.Errorf("store: record rollback drill: %w", err)
	}
	if err := paths.AppendJSONL(m.path(rollbackDrillFile), d); err != nil {
		return d, fmt.Errorf("store: record rollback drill: %w", err)
	}
	return d, nil
}

// readRoot reads one whole object out of s.
func readRoot(ctx context.Context, s Store, h core.Hash) ([]byte, error) {
	rc, err := s.Open(ctx, h)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rc.Close() }()
	return io.ReadAll(rc)
}

// readJSONLines reads a JSONL file into its raw lines. A missing file is no lines, not an error:
// an append-only log that has never been appended to is empty, which is exactly what a first run
// should see.
func readJSONLines(p string) ([][]byte, error) {
	f, err := os.Open(paths.Long(p))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("store: read %s: %w", p, err)
	}
	defer func() { _ = f.Close() }()

	var out [][]byte
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, scannerInitialBuf), maxJSONLLine)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		out = append(out, append([]byte(nil), line...))
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("store: read %s: %w", p, err)
	}
	return out, nil
}

// maxJSONLLine bounds one migration log line.
const maxJSONLLine = 4 << 20

// defaultMigrateConfig is the config a restored store is opened with when the caller supplied
// none. It is config.Defaults() rather than a zero Config because a zero Config has no chunk
// sizes and would open a store that cannot read anything.
func defaultMigrateConfig() config.Config { return config.Defaults() }

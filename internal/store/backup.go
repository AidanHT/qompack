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
	// lease and after the store had been flushed — which is what makes it a consistent backup
	// rather than a copy of a moving target.
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
// Consistency comes from three things, in this order: the single writer lease is held for the
// whole copy, so no other writer is running; the store is flushed first, so buffered writes are
// on disk before anything is read; and every copied byte is hashed as it is written, so the
// manifest describes what was actually captured rather than what was intended.
//
// A backup id is claimed exactly once. A second TakeBackup under the same id is os.ErrExist, not
// an overwrite: a backup is evidence, and silently replacing evidence is the failure mode the
// rollback requirement exists to prevent.
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
		b, ferr := os.ReadFile(p)
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

	b, err := json.Marshal(man)
	if err != nil {
		return BackupManifest{}, fmt.Errorf("store: encode backup manifest: %w", err)
	}
	if err := paths.WriteAtomic(filepath.Join(dir, backupManifestFile), b, 0o600); err != nil {
		return BackupManifest{}, err
	}
	return man, nil
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

// recordDrill appends the drill record and returns it. The log is append-only, so a later
// rehearsal can never overwrite an earlier one's finding.
func (m *Migrator) recordDrill(d RollbackDrill, refusal string) (RollbackDrill, error) {
	d.Refusal = refusal
	d.OK = refusal == ""
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
	sc.Buffer(make([]byte, 0, 64*1024), maxJSONLLine)
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

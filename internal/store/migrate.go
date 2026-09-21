package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
)

// Compatible legacy migration (SP-20 M1-04, requirement T20-M1-08).
//
// The plan states the contract in one paragraph: "Legacy import is side-by-side, versioned,
// idempotent, resumable, and bounded by a stable import frontier. Its cursor records source
// snapshot identity, last committed source position, destination mapping, and acknowledgement so
// restart cannot skip or duplicate a logical record. The legacy writer and importer hand off
// under one writer at a time. Legacy unknown remains legacy unknown. Old IDs and old readers
// remain available through the compatibility window. Before cutover, take a consistent
// engine-supported backup and prove parity for the declared import set. Before the first
// new-format write, demonstrate a compatible deployed reader or a verified backup restore; after
// that write, repeat the rollback drill against the actual new artifact without deleting
// evidence."
//
// Everything here is additive: nothing in this file mutates a legacy record, and nothing it
// writes is read by any existing store path. The originals are left exactly where they are and
// the imported copies live beside them, reachable by their legacy identity through the mapping
// log. ADR 0013's open consequence — "consistent backup and outer migration authority remain
// required" — is what backup.go and RehearseRollback answer.
//
// Two durability rules run through the whole file, both from the plan's required invariants:
//
//   - Publication order (invariant 3) is durable object, then reference, then committed frontier.
//     Import writes the object and flushes it, then records the tool_use reference, then appends
//     the mapping line, and only then commits the cursor. A crash between any two of those leaves
//     the frontier BEHIND the work, never ahead of it, which is the direction that cannot lose or
//     duplicate a record.
//   - The mapping log, not the cursor, is the durable import frontier. A cursor is one small file
//     and can be lost; the append-only mapping log is the record of what was actually imported,
//     and re-import consults it before it consults the cursor.

// ── on-disk names and versions ─────────────────────────────────────────────────────────────

const (
	// importCursorVersion is the version of migrate/cursor.json. Bump it only for a shape a v1
	// reader cannot honour; Cursor refuses a version it does not know rather than half-reading it,
	// matching config.MigrationSettingsVersion's rule for unknown future state.
	importCursorVersion = 1
	// importMappingVersion is the per-line version of migrate/mapping.jsonl. It is per-line, not
	// per-file, because the log is append-only: a v2 line may follow a v1 line in the same file.
	importMappingVersion = 1
	// handoffVersion is the version of migrate/handoff.json.
	handoffVersion = 1
	// parityReportVersion is the version of migrate/parity.json.
	parityReportVersion = 1
	// rollbackDrillVersion is the per-line version of migrate/rollback.jsonl.
	rollbackDrillVersion = 1
	// newFormatWriteVersion is the per-line version of migrate/newformat.jsonl.
	newFormatWriteVersion = 1
)

const (
	importCursorFile  = "cursor.json"
	importMappingFile = "mapping.jsonl"
	handoffFile       = "handoff.json"
	parityFile        = "parity.json"
	rollbackDrillFile = "rollback.jsonl"
	newFormatFile     = "newformat.jsonl"
	writerLockFile    = "writer.lock"
)

// defaultImportBatch is how many legacy records one Read asks for.
const defaultImportBatch = 64

// Errors this file reports. They are sentinel values because every one of them is a decision a
// caller has to distinguish: an incomplete import is resumable, a failed parity is not.
var (
	// ErrMigrationGateClosed is returned by NewMigrator while the legacy import/cutover build
	// gate has not passed. It is the default in every shipped build.
	ErrMigrationGateClosed = errors.New("store: legacy import/cutover gate is closed")
	// ErrSnapshotMismatch means the cursor on disk belongs to a different declared snapshot.
	ErrSnapshotMismatch = errors.New("store: import cursor belongs to a different legacy snapshot")
	// ErrCursorVersion means the cursor on disk was written by a build this one cannot read.
	ErrCursorVersion = errors.New("store: import cursor version is not readable by this build")
	// ErrImportIncomplete means the declared import set has not been fully imported.
	ErrImportIncomplete = errors.New("store: legacy import has not reached the declared frontier")
	// ErrHandoffUnstable means the legacy source moved after the import, so no handoff is stable.
	ErrHandoffUnstable = errors.New("store: legacy source moved past the declared frontier")
	// ErrWriterNotStopped means the legacy writer could not be stopped before the handoff.
	ErrWriterNotStopped = errors.New("store: the legacy writer was not stopped")
	// ErrWriterHeld means the single writer lease is already held.
	ErrWriterHeld = errors.New("store: the writer lease is already held")
	// ErrWriterNotQompack means writing has not been handed over to the new format yet, or has
	// already been handed away from the legacy writer.
	ErrWriterNotQompack = errors.New("store: this owner does not hold the writer after handoff")
	// ErrParityFailed means at least one parity check failed against the declared snapshot.
	ErrParityFailed = errors.New("store: parity failed against the declared import set")
)

// ── the legacy source ──────────────────────────────────────────────────────────────────────

// LegacyRecord is one logical record of a legacy store, as a LegacySource hands it over.
//
// Position is the source's own ordering, and it is what the cursor commits: it must be strictly
// increasing across a snapshot and stable across reads, or resumption cannot be exact. ID is the
// legacy identity, retained forever in the mapping log so an old id keeps resolving after
// cutover.
type LegacyRecord struct {
	ID       string
	Position int64
	Tool     string
	Path     string
	Session  core.SessionID
	Turn     core.TurnIndex
	TS       core.UnixMilli
	// Fidelity is the fidelity the LEGACY store declared. It is carried across unchanged and is
	// never recomputed from the imported bytes — see importFidelity.
	Fidelity core.Fidelity
	Payload  []byte
}

// LegacySnapshot is the declared, bounded import set: everything at or before Frontier.
type LegacySnapshot struct {
	// ID identifies the snapshot. A cursor is bound to it, so an import cannot silently continue
	// against a different source.
	ID string
	// Frontier is the last source position inside the declared snapshot, inclusive. It is the
	// "stable import frontier" the plan requires the import to be bounded by.
	Frontier int64
	// Records is how many logical records the snapshot declares, used by semantic parity.
	Records int64
}

// LegacySource is the read side of a legacy store. It is an interface, and deliberately a small
// one, because the shape of a legacy store is not this package's business: a caller supplies the
// reader for whatever format it is migrating from, and this file owns only the import contract.
type LegacySource interface {
	// Snapshot returns the declared import set. It is re-read before cutover to prove the legacy
	// writer stopped.
	Snapshot(ctx context.Context) (LegacySnapshot, error)
	// Read returns up to limit records with Position strictly greater than after, in ascending
	// Position order.
	Read(ctx context.Context, after int64, limit int) ([]LegacyRecord, error)
}

// ── cursor and mapping ─────────────────────────────────────────────────────────────────────

// ImportCursor is migrate/cursor.json: the durable resumption point.
type ImportCursor struct {
	Version int `json:"version"`
	// SnapshotID binds the cursor to one declared snapshot.
	SnapshotID string `json:"snapshot_id"`
	// Frontier is the declared snapshot's last position.
	Frontier int64 `json:"frontier"`
	// Position is the last COMMITTED source position: every record at or before it is durable in
	// the destination AND present in the mapping log. Resumption reads strictly after it.
	Position int64 `json:"position"`
	// Imported and Skipped are the running acknowledgement counts across all runs.
	Imported int64 `json:"imported"`
	Skipped  int64 `json:"skipped"`
	// Complete is true only once Position has reached Frontier.
	Complete  bool           `json:"complete"`
	UpdatedAt core.UnixMilli `json:"updated_at"`
}

// ImportMapping is one migrate/mapping.jsonl line: the identity map from a legacy record to its
// side-by-side copy. The originals are untouched; this is the only thing that relates them.
type ImportMapping struct {
	Version    int
	SnapshotID string
	LegacyID   string
	Position   int64
	Root       core.Hash
	ToolUseID  core.ToolUseID
	Tool       string
	Path       string
	Session    core.SessionID
	Turn       core.TurnIndex
	TS         core.UnixMilli
	// Fidelity is the legacy record's fidelity, carried through by importFidelity.
	Fidelity core.Fidelity
	// CanonBytes is the size of what was actually stored, which object parity re-reads.
	CanonBytes int64
}

// importMappingWire is ImportMapping's exact JSONL shape. Hashes and ids are written as strings
// rather than relying on any other package's JSON encoding, so a later reader of this log needs
// nothing but encoding/json and core.ParseHash.
type importMappingWire struct {
	Version    int            `json:"version"`
	SnapshotID string         `json:"snapshot_id"`
	LegacyID   string         `json:"legacy_id"`
	Position   int64          `json:"position"`
	Root       string         `json:"root"`
	ToolUseID  string         `json:"tool_use_id"`
	Tool       string         `json:"tool"`
	Path       string         `json:"path"`
	Session    string         `json:"session"`
	Turn       core.TurnIndex `json:"turn"`
	TS         core.UnixMilli `json:"ts"`
	Fidelity   string         `json:"fidelity"`
	CanonBytes int64          `json:"canon_bytes"`
}

// wire renders m for the mapping log.
func (m ImportMapping) wire() importMappingWire {
	return importMappingWire{
		Version: m.Version, SnapshotID: m.SnapshotID, LegacyID: m.LegacyID, Position: m.Position,
		Root: m.Root.String(), ToolUseID: string(m.ToolUseID), Tool: m.Tool, Path: m.Path,
		Session: string(m.Session), Turn: m.Turn, TS: m.TS,
		Fidelity: string(m.Fidelity), CanonBytes: m.CanonBytes,
	}
}

// mapping is the inverse of wire.
func (w importMappingWire) mapping() (ImportMapping, error) {
	h, err := core.ParseHash(w.Root)
	if err != nil {
		return ImportMapping{}, fmt.Errorf("store: mapping line for %s: %w", w.LegacyID, err)
	}
	return ImportMapping{
		Version: w.Version, SnapshotID: w.SnapshotID, LegacyID: w.LegacyID, Position: w.Position,
		Root: h, ToolUseID: core.ToolUseID(w.ToolUseID), Tool: w.Tool, Path: w.Path,
		Session: core.SessionID(w.Session), Turn: w.Turn, TS: w.TS,
		Fidelity: core.Fidelity(w.Fidelity), CanonBytes: w.CanonBytes,
	}, nil
}

// importFidelity is the whole of "legacy unknown remains legacy unknown".
//
// It passes a fidelity the legacy store actually declared straight through and maps everything
// else — an empty value, a spelling this build does not know, a value from a later format — to
// unknown. What it will not do, under any input, is PRODUCE core.FidelityExact: exact is a claim
// about the original capture ("the captured host delivery", core/evidence.go), and copying bytes
// faithfully out of a legacy store says nothing about how those bytes were captured. An importer
// that re-derived fidelity from the copy would manufacture evidence.
func importFidelity(f core.Fidelity) core.Fidelity {
	switch f {
	case core.FidelityExact, core.FidelityPrefix, core.FidelityPartial, core.FidelityRedacted,
		core.FidelityTruncated, core.FidelityBinary, core.FidelityFailure:
		return f
	default:
		return core.FidelityUnknown
	}
}

// ── the migrator ───────────────────────────────────────────────────────────────────────────

// MigrateOptions configures a Migrator. Its zero value is a CLOSED gate and no source, so a
// caller that forgets to think about the gate gets a refusal rather than a migration.
type MigrateOptions struct {
	// Source is the legacy store being imported.
	Source LegacySource
	// Gate is the legacy import/cutover build gate. Production wiring passes
	// config.LegacyImportGate(), which ships Passed false; the zero value is equally closed.
	Gate config.MigrationGate
	// Batch is how many legacy records one Read asks for; 0 means defaultImportBatch.
	Batch int
	// Clock stamps every durable record; nil means core.SystemClock().
	Clock core.Clock
	// Cfg is the config a RESTORED store is opened with during the rollback drill. Its zero value
	// means config.Defaults(): a zero Config has no chunk sizes and would open a store that
	// cannot read anything, so "unset" must not silently become "broken".
	Cfg config.Config
}

// Migrator owns one project's legacy migration: import, parity, writer handoff, cutover, backup
// and the rollback drill. It reaches the destination store only through the exported Store seam,
// so nothing in this file depends on the store's internal state.
type Migrator struct {
	s     Store
	root  string
	l     paths.Layout
	src   LegacySource
	batch int
	clock core.Clock
	cfg   config.Config

	// mu serialises the migrator's own on-disk state (cursor, mapping, handoff). The single
	// WRITER is a separate, cross-process concern; see AcquireWriter.
	mu sync.Mutex

	// afterBackupWalk runs between TakeBackup's copy walk and its refuseIfTheProjectMoved check.
	// It is unexported, nil in production and never set outside this package's tests: the hazard
	// that check exists for is a daemon sealing a delivery WHILE the walk runs, and a test has no
	// other way to place a write inside that window without a sleep, which this repo's tests may
	// not use. It is per-Migrator rather than a package variable so two tests running beside each
	// other cannot see one another's hook.
	afterBackupWalk func()
	// Operator maintenance supplies a bounded streaming copy. The legacy engine
	// retains its existing copy path when this optional implementation is nil.
	copyBackupFile func(context.Context, string, string) (int64, string, error)
}

// NewMigrator builds a Migrator over root, a PROJECT root (never <root>/.qompack), matching
// store.Open. It refuses outright while the gate is closed: there is no partially enabled state
// in which an import runs but a cutover does not, because an import that can never be completed
// is just an unowned copy of the data.
func NewMigrator(s Store, root string, o MigrateOptions) (*Migrator, error) {
	if !o.Gate.Passed {
		return nil, fmt.Errorf("%w: %s", ErrMigrationGateClosed, config.LegacyImportGateKey)
	}
	if s == nil {
		return nil, errors.New("store: NewMigrator needs a destination store")
	}
	if o.Source == nil {
		return nil, errors.New("store: NewMigrator needs a legacy source")
	}
	l := paths.Of(root)
	for _, d := range []string{l.Migrate, l.Backup} {
		if err := os.MkdirAll(paths.Long(d), 0o700); err != nil {
			return nil, fmt.Errorf("store: NewMigrator: mkdir %s: %w", d, err)
		}
	}
	m := &Migrator{s: s, root: root, l: l, src: o.Source, batch: o.Batch, clock: o.Clock, cfg: o.Cfg}
	if m.cfg.Runtime.Migration.SettingsVersion == 0 {
		m.cfg = defaultMigrateConfig()
	}
	if m.batch <= 0 {
		m.batch = defaultImportBatch
	}
	if m.clock == nil {
		m.clock = core.SystemClock()
	}
	return m, nil
}

func (m *Migrator) path(name string) string { return filepath.Join(m.l.Migrate, name) }

// retainRoots declares hs as GC retention roots of the rollback class (SP-20 invariant 9: GC
// cannot collect a root rollback material needs).
//
// migrate/mapping.jsonl, migrate/rollback.jsonl and backup/<id>/manifest.json are NOT among the
// files GC harvests hashes from, and they deliberately are not: gcRootFiles is a fixed list and
// migration is a producer like any other, so it declares what it needs through the generic
// AppendRetentionRoot convention instead. Without that declaration every imported object is
// collectible the moment the retention window passes, and "old ids keep working" — the whole point
// of a side-by-side import — breaks the first time GC runs.
//
// The declaration is durable and append-only, and it is made BEFORE the artifact that references
// the hash is written, so a crash between the two leaves an over-retained object rather than an
// unprotected one. A zero hash is skipped rather than refused: it names nothing, so there is
// nothing to retain, and failing a whole import over one would be a worse trade.
func (m *Migrator) retainRoots(reason string, hs ...core.Hash) error {
	for _, h := range hs {
		if h.IsZero() {
			continue
		}
		if err := AppendRetentionRoot(m.root, RetentionRoot{
			Hash: h, Class: RetentionRollback, Reason: reason,
		}); err != nil {
			return fmt.Errorf("store: declare retention root %s: %w", h.Short(), err)
		}
	}
	return nil
}

func (m *Migrator) now() core.UnixMilli { return core.NowMilli(m.clock) }

// Cursor reads the durable import cursor. A missing cursor is the zero cursor, not an error: an
// import that has never run and an import whose cursor was lost are the same starting point, and
// the mapping log is what keeps them from duplicating.
func (m *Migrator) Cursor() (ImportCursor, error) {
	b, err := os.ReadFile(paths.Long(m.path(importCursorFile)))
	if os.IsNotExist(err) {
		return ImportCursor{Version: importCursorVersion}, nil
	}
	if err != nil {
		return ImportCursor{}, fmt.Errorf("store: read import cursor: %w", err)
	}
	var cur ImportCursor
	if err := json.Unmarshal(b, &cur); err != nil {
		return ImportCursor{}, fmt.Errorf("store: parse import cursor: %w", err)
	}
	if cur.Version != importCursorVersion {
		return ImportCursor{}, fmt.Errorf("%w: found %d, this build reads %d",
			ErrCursorVersion, cur.Version, importCursorVersion)
	}
	return cur, nil
}

// writeCursor commits the cursor atomically. This is the frontier commit in the publication
// order: it runs last, after the object is durable and the mapping line is on disk.
func (m *Migrator) writeCursor(cur ImportCursor) error {
	cur.Version = importCursorVersion
	cur.UpdatedAt = m.now()
	b, err := json.Marshal(cur)
	if err != nil {
		return fmt.Errorf("store: encode import cursor: %w", err)
	}
	return paths.WriteAtomic(m.path(importCursorFile), b, 0o600)
}

// Frontier reads the mapping log into legacy-id order. The LAST line for an id wins, so a
// corrective line appended later supersedes an earlier one without rewriting the log.
func (m *Migrator) Frontier() (map[string]ImportMapping, []ImportMapping, error) {
	lines, err := readJSONLines(m.path(importMappingFile))
	if err != nil {
		return nil, nil, err
	}
	byID := make(map[string]ImportMapping, len(lines))
	order := make([]ImportMapping, 0, len(lines))
	for _, raw := range lines {
		var w importMappingWire
		if err := json.Unmarshal(raw, &w); err != nil {
			return nil, nil, fmt.Errorf("store: parse mapping line: %w", err)
		}
		mp, err := w.mapping()
		if err != nil {
			return nil, nil, err
		}
		if _, seen := byID[mp.LegacyID]; !seen {
			order = append(order, mp)
		}
		byID[mp.LegacyID] = mp
	}
	// order holds first-appearance order; refresh each entry to the winning line.
	for i := range order {
		order[i] = byID[order[i].LegacyID]
	}
	return byID, order, nil
}

// LookupLegacy resolves a legacy id to its imported copy. This is what keeps an OLD ID working:
// after cutover, a caller holding nothing but a legacy identity still finds the object.
func (m *Migrator) LookupLegacy(legacyID string) (ImportMapping, bool, error) {
	byID, _, err := m.Frontier()
	if err != nil {
		return ImportMapping{}, false, err
	}
	mp, ok := byID[legacyID]
	return mp, ok, nil
}

// ImportReport is one Import call's outcome.
type ImportReport struct {
	Cursor   ImportCursor
	Imported int
	Skipped  int
	Complete bool
}

// Import copies the declared snapshot into the destination store, side by side.
//
// It is resumable (it starts strictly after the committed cursor position), idempotent (a record
// already in the mapping log is skipped, not re-mapped), and bounded (it never reads past the
// declared frontier). A completed import is a no-op: it does not even read the source.
func (m *Migrator) Import(ctx context.Context) (ImportReport, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	snap, err := m.src.Snapshot(ctx)
	if err != nil {
		return ImportReport{}, fmt.Errorf("store: legacy snapshot: %w", err)
	}
	cur, err := m.Cursor()
	if err != nil {
		return ImportReport{}, err
	}
	if cur.SnapshotID != "" && cur.SnapshotID != snap.ID {
		return ImportReport{}, fmt.Errorf("%w: cursor holds %q, source declares %q",
			ErrSnapshotMismatch, cur.SnapshotID, snap.ID)
	}
	if cur.Complete && cur.Frontier == snap.Frontier {
		return ImportReport{Cursor: cur, Complete: true}, nil
	}
	cur.SnapshotID, cur.Frontier = snap.ID, snap.Frontier

	byID, _, err := m.Frontier()
	if err != nil {
		return ImportReport{}, err
	}

	rep := ImportReport{}
	for cur.Position < snap.Frontier {
		batch, rerr := m.src.Read(ctx, cur.Position, m.batch)
		if rerr != nil {
			rep.Cursor = cur
			return rep, fmt.Errorf("store: read legacy records after %d: %w", cur.Position, rerr)
		}
		if len(batch) == 0 {
			break
		}
		for _, r := range batch {
			if r.Position > snap.Frontier {
				break // bounded by the declared frontier, whatever the source offers
			}
			if _, done := byID[r.ID]; done {
				// Already on the durable frontier. This is the crash-between-mapping-and-cursor
				// case, and the lost-cursor case, and the plain re-run case: all three are the
				// same skip, which is what makes the import idempotent.
				cur.Position, cur.Skipped, rep.Skipped = r.Position, cur.Skipped+1, rep.Skipped+1
				continue
			}
			mp, ierr := m.importOne(ctx, snap, r)
			if ierr != nil {
				rep.Cursor = cur
				_ = m.writeCursor(cur)
				return rep, ierr
			}
			byID[r.ID] = mp
			cur.Position, cur.Imported, rep.Imported = r.Position, cur.Imported+1, rep.Imported+1
		}
		if werr := m.writeCursor(cur); werr != nil {
			rep.Cursor = cur
			return rep, werr
		}
	}

	cur.Complete = cur.Position >= snap.Frontier
	if err := m.writeCursor(cur); err != nil {
		return rep, err
	}
	rep.Cursor, rep.Complete = cur, cur.Complete
	return rep, nil
}

// importOne performs the publication order for a single legacy record: durable object, then
// reference, then the mapping line that puts it on the frontier. The cursor is committed by the
// caller after the batch, so a crash here leaves the frontier behind the work — recoverable —
// rather than ahead of it.
func (m *Migrator) importOne(ctx context.Context, snap LegacySnapshot, r LegacyRecord) (ImportMapping, error) {
	// 1. durable object.
	res, err := m.s.PutBytes(ctx, r.Payload, PutOptions{Tool: r.Tool, Path: r.Path})
	if err != nil {
		return ImportMapping{}, fmt.Errorf("store: import %s: put: %w", r.ID, err)
	}
	if err := m.s.Flush(ctx); err != nil {
		return ImportMapping{}, fmt.Errorf("store: import %s: flush: %w", r.ID, err)
	}

	// 2. retention. The object is declared a rollback-class retention root BEFORE anything points
	// at it, so GC can never collect an object the mapping log is about to name.
	if err := m.retainRoots("referenced by migrate/"+importMappingFile+" for legacy id "+r.ID,
		res.Root.Hash); err != nil {
		return ImportMapping{}, fmt.Errorf("store: import %s: %w", r.ID, err)
	}

	// 3. reference. The destination id embeds the legacy id, so the relationship survives even
	// for a reader that has only the tool_use index and not the mapping log.
	id := legacyToolUseID(r.ID)
	args, preview := ArgsDigest(json.RawMessage(fmt.Sprintf(`{"legacy_id":%q,"legacy_path":%q}`, r.ID, r.Path)))
	if err := m.s.RecordToolUse(ctx, ToolUseRecord{
		ID: id, Session: r.Session, Turn: r.Turn, TS: r.TS, Tool: r.Tool,
		ArgsDigest: args, ArgsPreview: preview,
		Root: res.Root.Hash, Path: r.Path, Bytes: res.Root.RawBytes, Tokens: res.Root.Tokens,
		Signature: res.Signature,
	}); err != nil {
		return ImportMapping{}, fmt.Errorf("store: import %s: record tool use: %w", r.ID, err)
	}

	// 4. frontier line.
	mp := ImportMapping{
		Version: importMappingVersion, SnapshotID: snap.ID, LegacyID: r.ID, Position: r.Position,
		Root: res.Root.Hash, ToolUseID: id, Tool: r.Tool, Path: r.Path,
		Session: r.Session, Turn: r.Turn, TS: r.TS,
		Fidelity: importFidelity(r.Fidelity), CanonBytes: res.Root.CanonBytes,
	}
	if err := paths.AppendJSONL(m.path(importMappingFile), mp.wire()); err != nil {
		return ImportMapping{}, fmt.Errorf("store: import %s: append mapping: %w", r.ID, err)
	}
	return mp, nil
}

// legacyToolUseID derives the destination reference id from a legacy id. It is deterministic, so
// a re-import of the same record produces the same id and RecordToolUse's own idempotence applies.
func legacyToolUseID(legacyID string) core.ToolUseID {
	return core.ToolUseID("legacy_" + legacyID)
}

// ── parity ─────────────────────────────────────────────────────────────────────────────────

// The four declared parity checks. They are named constants because a report that silently lost
// one would still say OK.
const (
	parityObject    = "object"
	parityReference = "reference"
	parityQuery     = "query"
	paritySemantic  = "semantic"
)

// ParityCheck is one check's outcome. Mismatches is capped so a wholesale failure cannot produce
// an unbounded report.
type ParityCheck struct {
	Name       string   `json:"name"`
	Checked    int64    `json:"checked"`
	OK         bool     `json:"ok"`
	Mismatches []string `json:"mismatches,omitempty"`
}

// maxParityMismatches bounds one check's reported mismatches.
const maxParityMismatches = 20

func (c *ParityCheck) fail(format string, args ...any) {
	c.OK = false
	if len(c.Mismatches) < maxParityMismatches {
		c.Mismatches = append(c.Mismatches, fmt.Sprintf(format, args...))
	}
}

// ParityReport is migrate/parity.json: the evidence a cutover cites.
type ParityReport struct {
	Version    int            `json:"version"`
	SnapshotID string         `json:"snapshot_id"`
	Frontier   int64          `json:"frontier"`
	Mapped     int64          `json:"mapped"`
	Checks     []ParityCheck  `json:"checks"`
	OK         bool           `json:"ok"`
	At         core.UnixMilli `json:"at"`
	// Digest is a sha256 over the checks, so a handoff record can cite the exact parity result
	// that authorised it rather than merely asserting that one passed.
	Digest string `json:"digest"`
}

// Parity runs all four declared checks against the declared snapshot and writes the report.
//
// object    — every mapped root is present and re-reads at its recorded size.
// reference — every mapped tool_use resolves, points at the mapped root, and is not superseded.
// query     — every mapped reference is reachable through the ordinary path query surface, so the
//
//	copies are not merely stored but findable the way real callers find things.
//
// semantic  — the destination still MEANS what the source declared: same record set, same
//
//	tool/path/session/turn/timestamp, and a fidelity that is exactly what
//	importFidelity yields for the source value. This is the check that catches an
//	upgraded fidelity, which no byte-level comparison ever would.
func (m *Migrator) Parity(ctx context.Context) (ParityReport, error) {
	snap, err := m.src.Snapshot(ctx)
	if err != nil {
		return ParityReport{}, fmt.Errorf("store: legacy snapshot: %w", err)
	}
	_, order, err := m.Frontier()
	if err != nil {
		return ParityReport{}, err
	}

	rep := ParityReport{
		Version: parityReportVersion, SnapshotID: snap.ID, Frontier: snap.Frontier,
		Mapped: int64(len(order)), At: m.now(),
	}
	obj := ParityCheck{Name: parityObject, OK: true}
	ref := ParityCheck{Name: parityReference, OK: true}
	qry := ParityCheck{Name: parityQuery, OK: true}
	sem := ParityCheck{Name: paritySemantic, OK: true}

	for _, mp := range order {
		obj.Checked++
		// GetRoot, not Has: Has answers for a CHUNK hash, and a root hash is the Merkle root over
		// its chunks, so Has(root) is false for a perfectly healthy object. The root index is what
		// says an object exists, and it is also what a GC tombstone removes.
		if _, gerr := m.s.GetRoot(ctx, mp.Root); gerr != nil {
			obj.fail("%s: object %s is absent: %v", mp.LegacyID, mp.Root.Short(), gerr)
		} else if n, rerr := m.objectSize(ctx, mp.Root); rerr != nil {
			obj.fail("%s: object %s does not re-read: %v", mp.LegacyID, mp.Root.Short(), rerr)
		} else if n != mp.CanonBytes {
			obj.fail("%s: object %s re-read %d bytes, mapped %d", mp.LegacyID, mp.Root.Short(), n, mp.CanonBytes)
		}

		ref.Checked++
		rec, rerr := m.s.ToolUse(ctx, mp.ToolUseID)
		switch {
		case rerr != nil:
			ref.fail("%s: reference %s does not resolve: %v", mp.LegacyID, mp.ToolUseID, rerr)
		case rec.Root != mp.Root:
			ref.fail("%s: reference %s points at %s, mapped %s", mp.LegacyID, mp.ToolUseID, rec.Root.Short(), mp.Root.Short())
		case rec.Status != StatusOK:
			ref.fail("%s: reference %s is superseded", mp.LegacyID, mp.ToolUseID)
		case rec.Path != mp.Path || rec.Tool != mp.Tool:
			ref.fail("%s: reference %s has tool/path %s/%s, mapped %s/%s", mp.LegacyID, mp.ToolUseID, rec.Tool, rec.Path, mp.Tool, mp.Path)
		}

		qry.Checked++
		hits, qerr := m.s.ToolUsesByPath(ctx, mp.Path, maxParityQueryHits)
		if qerr != nil {
			qry.fail("%s: query on %s failed: %v", mp.LegacyID, mp.Path, qerr)
		} else if !containsToolUse(hits, mp.ToolUseID) {
			qry.fail("%s: reference %s is not reachable by a query on %s", mp.LegacyID, mp.ToolUseID, mp.Path)
		}
	}

	if err := m.semanticParity(ctx, snap, order, &sem); err != nil {
		return ParityReport{}, err
	}

	rep.Checks = []ParityCheck{obj, ref, qry, sem}
	rep.OK = obj.OK && ref.OK && qry.OK && sem.OK
	rep.Digest = parityDigest(rep.Checks)

	b, err := json.Marshal(rep)
	if err != nil {
		return ParityReport{}, fmt.Errorf("store: encode parity report: %w", err)
	}
	if err := paths.WriteAtomic(m.path(parityFile), b, 0o600); err != nil {
		return ParityReport{}, err
	}
	return rep, nil
}

// maxParityQueryHits bounds the per-path query parity check.
const maxParityQueryHits = 1000

func containsToolUse(recs []ToolUseRecord, id core.ToolUseID) bool {
	for _, r := range recs {
		if r.ID == id {
			return true
		}
	}
	return false
}

// objectSizeReadBuf sizes objectSize's read buffer, in the shift spelling this package already
// uses for scannerInitialBuf and scannerMaxBuf. It bounds one Read call, never the object: the
// loop below streams a root of any size through it, so it is a working-set choice and not a budget
// any configuration owns.
const objectSizeReadBuf = 32 << 10

// objectSize re-reads a root end to end and reports how many bytes came back. The store verifies
// every chunk's plaintext against its hash on the way out, so a successful read is itself the
// integrity proof; the byte count is what pins it to the mapping.
func (m *Migrator) objectSize(ctx context.Context, h core.Hash) (int64, error) {
	rc, err := m.s.Open(ctx, h)
	if err != nil {
		return 0, err
	}
	defer func() { _ = rc.Close() }()
	buf := make([]byte, objectSizeReadBuf)
	var n int64
	for {
		r, rerr := rc.Read(buf)
		n += int64(r)
		if rerr != nil {
			if errors.Is(rerr, os.ErrClosed) {
				return n, rerr
			}
			break
		}
	}
	return n, nil
}

// semanticParity walks the declared snapshot and compares meaning, not bytes.
func (m *Migrator) semanticParity(ctx context.Context, snap LegacySnapshot, order []ImportMapping, sem *ParityCheck) error {
	byID := make(map[string]ImportMapping, len(order))
	for _, mp := range order {
		byID[mp.LegacyID] = mp
	}
	var after int64
	seen := map[string]bool{}
	for after < snap.Frontier {
		batch, err := m.src.Read(ctx, after, defaultImportBatch)
		if err != nil {
			return fmt.Errorf("store: semantic parity: read legacy records after %d: %w", after, err)
		}
		if len(batch) == 0 {
			break
		}
		for _, r := range batch {
			if r.Position > snap.Frontier {
				break
			}
			after = r.Position
			seen[r.ID] = true
			sem.Checked++
			mp, ok := byID[r.ID]
			if !ok {
				sem.fail("%s: declared by the snapshot but never imported", r.ID)
				continue
			}
			if mp.Position != r.Position || mp.Tool != r.Tool || mp.Path != r.Path ||
				mp.Session != r.Session || mp.Turn != r.Turn || mp.TS != r.TS {
				sem.fail("%s: imported record does not match the declared snapshot (tool/path/session/turn/ts)", r.ID)
			}
			if want := importFidelity(r.Fidelity); mp.Fidelity != want {
				sem.fail("%s: fidelity is %q, the declared snapshot supports only %q", r.ID, mp.Fidelity, want)
			}
		}
	}
	for _, mp := range order {
		if !seen[mp.LegacyID] {
			sem.fail("%s: imported but not in the declared snapshot", mp.LegacyID)
		}
	}
	return nil
}

// parityDigest is a stable sha256 over the checks.
func parityDigest(checks []ParityCheck) string {
	b, err := json.Marshal(checks)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// ── writer handoff and cutover ─────────────────────────────────────────────────────────────

// WriterOwner names who may write. There are exactly two, and after cutover exactly one of them
// is valid.
type WriterOwner string

const (
	// WriterLegacy is the legacy writer, which owns writing until cutover.
	WriterLegacy WriterOwner = "legacy"
	// WriterQompack is the new-format writer, which owns writing after cutover.
	WriterQompack WriterOwner = "qompack"
)

// Handoff is migrate/handoff.json: who owns writing, and the evidence that authorised the
// transfer. Its zero state — no file — means the legacy writer still owns writing.
type Handoff struct {
	Version    int         `json:"version"`
	Owner      WriterOwner `json:"owner"`
	SnapshotID string      `json:"snapshot_id"`
	Frontier   int64       `json:"frontier"`
	// ParityDigest and BackupID cite the exact parity report and backup the cutover stood on.
	ParityDigest string         `json:"parity_digest"`
	BackupID     string         `json:"backup_id"`
	At           core.UnixMilli `json:"at"`
	// FirstNewWriteAt is stamped by the first RecordNewFormatWrite, which is the moment the
	// rollback drill has to be repeated against a real new artifact.
	FirstNewWriteAt core.UnixMilli `json:"first_new_write_at,omitempty"`
}

// Handoff reads the handoff record.
func (m *Migrator) Handoff() (Handoff, error) {
	b, err := os.ReadFile(paths.Long(m.path(handoffFile)))
	if os.IsNotExist(err) {
		return Handoff{Version: handoffVersion, Owner: WriterLegacy}, nil
	}
	if err != nil {
		return Handoff{}, fmt.Errorf("store: read handoff: %w", err)
	}
	var h Handoff
	if err := json.Unmarshal(b, &h); err != nil {
		return Handoff{}, fmt.Errorf("store: parse handoff: %w", err)
	}
	if h.Owner == "" {
		h.Owner = WriterLegacy
	}
	return h, nil
}

func (m *Migrator) writeHandoff(h Handoff) error {
	h.Version = handoffVersion
	b, err := json.Marshal(h)
	if err != nil {
		return fmt.Errorf("store: encode handoff: %w", err)
	}
	return paths.WriteAtomic(m.path(handoffFile), b, 0o600)
}

// WriterLease is the exclusive right to write. It exists as a file, not a mutex, because the two
// writers being kept apart are processes, not goroutines.
type WriterLease struct {
	path  string
	owner WriterOwner
	once  sync.Once
}

// Release gives the lease back. It is idempotent.
func (w *WriterLease) Release() error {
	var err error
	w.once.Do(func() {
		if rerr := os.Remove(paths.Long(w.path)); rerr != nil && !os.IsNotExist(rerr) {
			err = rerr
		}
	})
	return err
}

// AcquireWriter takes the single writer lease for owner.
//
// Two things have to hold, and they are different things. The lease must be free — that is what
// "exactly one writer" means at any instant. And owner must be the one the handoff record names —
// that is what "the handoff transferred writing" means. Before cutover only the legacy writer may
// acquire; after it, only the new-format writer, permanently.
func (m *Migrator) AcquireWriter(owner WriterOwner) (*WriterLease, error) {
	h, err := m.Handoff()
	if err != nil {
		return nil, err
	}
	if owner != h.Owner {
		return nil, fmt.Errorf("%w: %s owns writing, %s asked", ErrWriterNotQompack, h.Owner, owner)
	}
	p := m.path(writerLockFile)
	f, err := paths.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if os.IsExist(err) {
			return nil, fmt.Errorf("%w: %s", ErrWriterHeld, p)
		}
		return nil, fmt.Errorf("store: acquire writer lease: %w", err)
	}
	_, _ = fmt.Fprintf(f, "%s\n", owner)
	_ = f.Close()
	return &WriterLease{path: p, owner: owner}, nil
}

// CutoverOptions configures a cutover.
type CutoverOptions struct {
	// BackupID names a backup that must already exist and verify. The plan requires a consistent
	// engine-supported backup BEFORE cutover, so a cutover with no verified backup is refused
	// rather than merely warned about.
	BackupID string
	// StopLegacyWriter stops the legacy writer. It is required, and its error is fatal: a handoff
	// that cannot stop the outgoing writer is not a handoff, it is two writers.
	StopLegacyWriter func(context.Context) error
}

// Cutover transfers writing from the legacy writer to the new format, and only then.
//
// Every refusal below is a precondition of the plan's own sentence, in order: the import must
// have reached the declared frontier; the source must not have moved past it (otherwise the
// handoff is not stable and records would be stranded); the legacy writer must actually stop; a
// consistent backup must exist and verify; and parity must pass for the declared import set. A
// failure at any step leaves the handoff record untouched, so the legacy writer keeps writing.
func (m *Migrator) Cutover(ctx context.Context, o CutoverOptions) (Handoff, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	cur, err := m.Cursor()
	if err != nil {
		return Handoff{}, err
	}
	if !cur.Complete {
		return Handoff{}, fmt.Errorf("%w: cursor at %d of %d", ErrImportIncomplete, cur.Position, cur.Frontier)
	}
	snap, err := m.src.Snapshot(ctx)
	if err != nil {
		return Handoff{}, fmt.Errorf("store: legacy snapshot: %w", err)
	}
	if snap.ID != cur.SnapshotID {
		return Handoff{}, fmt.Errorf("%w: cursor holds %q, source declares %q",
			ErrSnapshotMismatch, cur.SnapshotID, snap.ID)
	}
	if snap.Frontier != cur.Frontier {
		return Handoff{}, fmt.Errorf("%w: imported through %d, source now declares %d",
			ErrHandoffUnstable, cur.Frontier, snap.Frontier)
	}
	if o.StopLegacyWriter == nil {
		return Handoff{}, fmt.Errorf("%w: no way to stop it was supplied", ErrWriterNotStopped)
	}
	if err := o.StopLegacyWriter(ctx); err != nil {
		return Handoff{}, fmt.Errorf("%w: %v", ErrWriterNotStopped, err)
	}
	man, err := m.VerifyBackup(o.BackupID)
	if err != nil {
		return Handoff{}, fmt.Errorf("store: cutover needs a verified consistent backup: %w", err)
	}
	par, err := m.Parity(ctx)
	if err != nil {
		return Handoff{}, err
	}
	if !par.OK {
		return Handoff{}, fmt.Errorf("%w: %+v", ErrParityFailed, par.Checks)
	}

	h := Handoff{
		Owner: WriterQompack, SnapshotID: snap.ID, Frontier: snap.Frontier,
		ParityDigest: par.Digest, BackupID: man.ID, At: m.now(),
	}
	if err := m.writeHandoff(h); err != nil {
		return Handoff{}, err
	}
	h.Version = handoffVersion
	return h, nil
}

// ── new-format writes ──────────────────────────────────────────────────────────────────────

// NewFormatWrite is one migrate/newformat.jsonl line: a write made in the new format after
// cutover. The log exists so the rollback drill can enumerate exactly what an older binary would
// not be able to read, instead of guessing or claiming there is nothing.
type NewFormatWrite struct {
	Version int `json:"version"`
	// Root is the new artifact's object identity, as core.Hash.String().
	Root string `json:"root"`
	// LegacyID is the legacy record this write supersedes, or "" for a write with no legacy
	// counterpart at all — which is the case an older binary is least able to interpret.
	LegacyID string         `json:"legacy_id,omitempty"`
	At       core.UnixMilli `json:"at"`
	// First marks the first new-format write, the one that ends the "before" rollback phase.
	First bool `json:"first"`
}

// RecordNewFormatWrite registers a new-format write. It refuses unless the handoff has actually
// transferred writing, which is what makes "after cutover, exactly one writer" enforceable rather
// than merely declared.
func (m *Migrator) RecordNewFormatWrite(ctx context.Context, root core.Hash, legacyID string) (NewFormatWrite, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return NewFormatWrite{}, err
	}

	h, err := m.Handoff()
	if err != nil {
		return NewFormatWrite{}, err
	}
	if h.Owner != WriterQompack {
		return NewFormatWrite{}, fmt.Errorf("%w: %s still owns writing", ErrWriterNotQompack, h.Owner)
	}
	prior, err := m.NewFormatWrites()
	if err != nil {
		return NewFormatWrite{}, err
	}
	w := NewFormatWrite{
		Version: newFormatWriteVersion, Root: root.String(), LegacyID: legacyID,
		At: m.now(), First: len(prior) == 0,
	}
	if err := paths.AppendJSONL(m.path(newFormatFile), w); err != nil {
		return NewFormatWrite{}, fmt.Errorf("store: record new-format write: %w", err)
	}
	if w.First {
		h.FirstNewWriteAt = w.At
		if err := m.writeHandoff(h); err != nil {
			return NewFormatWrite{}, err
		}
	}
	return w, nil
}

// NewFormatWrites reads the new-format write log.
func (m *Migrator) NewFormatWrites() ([]NewFormatWrite, error) {
	lines, err := readJSONLines(m.path(newFormatFile))
	if err != nil {
		return nil, err
	}
	out := make([]NewFormatWrite, 0, len(lines))
	for _, raw := range lines {
		var w NewFormatWrite
		if err := json.Unmarshal(raw, &w); err != nil {
			return nil, fmt.Errorf("store: parse new-format write line: %w", err)
		}
		out = append(out, w)
	}
	return out, nil
}

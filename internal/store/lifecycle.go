package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/qompack/qompack/internal/canon"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
)

// SP-20 M1-03 lifecycle contracts: exact delta bases (invariant 6) and the retention-root classes
// GC may never collect through (invariant 9).
//
// Everything here is ADDITIVE. The §5.8 Store interface is unchanged, the roots.jsonl line shape
// grows two optional keys that a v1 reader simply does not see, and the new registry/producer
// files are read only when they exist — waves that have not shipped their producer yet are
// indistinguishable from a project that never used one.

// Fidelity is the explicit recovery status of one stored root's ORIGINAL, pre-canonicalization
// bytes (Qompack.md §8.1; SP-20 invariant 6: "no base means no delta-only recovery claim").
//
// The distinction that matters is between "these bytes ARE the original" and "these bytes are what
// survived canonicalization". Reporting the second as the first is the silent-partial outcome the
// invariant forbids, so every read path returns one of these alongside its bytes.
type Fidelity string

const (
	// FidelityExact means the original was reproduced byte-for-byte: either canonicalization
	// changed nothing, or a delta record with a durable declared base round-tripped exactly.
	FidelityExact Fidelity = "exact"
	// FidelityFull means the original was retained as its own full object because an exact delta
	// could not be proven. The bytes are still exact; the representation is not a delta.
	FidelityFull Fidelity = "full"
	// FidelityCanonical means only canonicalized bytes exist. They are NOT the original and must
	// never be presented as one.
	FidelityCanonical Fidelity = "canonical"
	// FidelityUnavailable means a recovery record was declared but its base, its payload or its
	// object is absent.
	FidelityUnavailable Fidelity = "unavailable"
	// FidelityCorrupt means a recovery record was found but cannot be applied.
	FidelityCorrupt Fidelity = "corrupt"
)

// ErrDeltaBaseUnavailable reports a delta record whose declared base is not in service.
var ErrDeltaBaseUnavailable = errors.New("qompack: delta base unavailable")

// ErrDeltaCorrupt reports a delta record that was found but cannot be applied to its base.
var ErrDeltaCorrupt = errors.New("qompack: delta record corrupt")

// DeltaRecord is one stored canonicalization side record together with the base it declares.
//
// Base is populated even on a failed read, so a caller can report WHICH base was claimed and
// missing rather than reporting an anonymous absence.
type DeltaRecord struct {
	// Root is the delta record's own root hash.
	Root core.Hash
	// Base is the content root this record reconstructs. A zero Base is a legacy record written
	// before SP-20 declared bases; it supports no recovery claim.
	Base core.Hash
	// Deltas is the volatile side record itself.
	Deltas []canon.Delta
}

// RetentionClass names why a producer needs an object held live. It is the vocabulary of SP-20's
// invariant 9: "GC cannot collect a root needed by a lease, delta, pending write, checkpoint,
// evidence reference, or rollback record."
type RetentionClass string

const (
	// RetentionLease is an open delivery lease: work assigned and not yet acknowledged.
	RetentionLease RetentionClass = "lease"
	// RetentionPending is a written-but-not-yet-rooted object.
	RetentionPending RetentionClass = "pending_write"
	// RetentionCheckpoint is a committed checkpoint's reference.
	RetentionCheckpoint RetentionClass = "checkpoint"
	// RetentionPin is a pinned invariant's reference.
	RetentionPin RetentionClass = "pin"
	// RetentionEvidence is an evidence record's reference (eliminations, observation evidence).
	RetentionEvidence RetentionClass = "evidence"
	// RetentionDeltaBase is the base a delta record declares, or the delta record a base owns.
	RetentionDeltaBase RetentionClass = "delta_base"
	// RetentionRollback is rollback or backup material for a migration.
	RetentionRollback RetentionClass = "rollback"
)

// RetentionRoot is one hash a producer needs retained, with the class and reason GC reports.
type RetentionRoot struct {
	Hash   core.Hash      `json:"hash"`
	Class  RetentionClass `json:"class"`
	Reason string         `json:"reason"`
}

// RetentionRootSource is the in-process seam a producer of live references satisfies so GC holds
// its objects live WITHOUT internal/store importing the producer's package.
//
// internal/daemon, internal/checkpoint and internal/negknow all import store, so store cannot
// import them back (00-ARCHITECTURE.md §3.2). A producer therefore either implements this
// interface and is handed to Open through Deps.RetentionRoots, or appends to the on-disk file
// AppendRetentionRoot writes — the two are equivalent, and GC consults both.
//
// A source that reports an error is NOT treated as "nothing to retain": the pass logs the failure
// and refuses to collect this round, because an unreadable lease set is indistinguishable from a
// full one and under-retention is data loss.
type RetentionRootSource interface {
	RetentionRoots(ctx context.Context) ([]RetentionRoot, error)
}

// The on-disk root-file convention. Every one of these is OPTIONAL: a missing file means the
// producer has not shipped, never that collection is unsafe.
const (
	// deliveryLeaseFile is internal/daemon's delivery-lease journal, under <root>/.qompack/state.
	// GC reads it structurally — every hash-shaped token in an OPEN lease line retains — so the
	// daemon needs no store-side call and store needs no daemon import. The journal is append-only
	// and carries no release record, so a line stops being an open lease only by being named in
	// deliveryAckFile.
	deliveryLeaseFile = "delivery-leases.jsonl"
	// deliveryAckFile is internal/daemon's committed-frontier journal, beside deliveryLeaseFile.
	// One line per delivery that reached committed publication — durable object, verified
	// reference, then frontier — naming the delivery nonce its lease line carries.
	//
	// It is what makes RetentionLease a BOUNDED class. Without it every delivery a daemon ever
	// handled would pin its references forever, because an append-only assignment journal alone
	// cannot say which assignments are finished; SP-20 invariant 9 retains what a lease NEEDS, not
	// everything a lease ever touched. An acknowledged delivery's evidence is still retained — by
	// the evidence-class root WriteCaptureSidecar declares for it — so closing a lease releases the
	// lease's claim and nothing else's.
	deliveryAckFile = "delivery-acks.jsonl"
	// retentionRootsFile is the generic producer convention under <root>/.qompack/state: one
	// RetentionRoot JSON object per line, written by AppendRetentionRoot. Rollback/backup
	// material and any other producer without a journal of its own declares itself here.
	retentionRootsFile = "retention-roots.jsonl"
	// evidenceRootsFile is the evidence-reference file under <root>/.qompack/records.
	evidenceRootsFile = "evidence.jsonl"
	// eliminationsFile is negknow's elimination evidence, under <root>/.qompack/records.
	eliminationsFile = "eliminations.jsonl"
	// invariantsFile is the pin file under <root>/.qompack/pins.
	invariantsFile = "invariants.jsonl"
	// pendingWriteDir is the durable pending-write registry under <root>/.qompack/state: one
	// marker file per in-flight Put, naming the chunks it is about to write.
	pendingWriteDir = "pending"
	// pendingWriteSuffix is the marker file extension.
	pendingWriteSuffix = ".json"
)

// pendingRecordVersion is the "v" a pending-write marker carries. It is its own version because
// the registry is a new file, independent of indexRecordVersion's roots.jsonl lineage.
const pendingRecordVersion = 1

// pendingNameRandBytes is how many random bytes a marker filename carries, rendered as 12 hex
// characters — the same construction tmpObjectPath uses, and for the same reason: two concurrent
// Puts of the same content must not share a marker.
const pendingNameRandBytes = 6

// pendingWire is one pending-write marker.
type pendingWire struct {
	V      int            `json:"v"`
	Root   string         `json:"root"`
	TS     core.UnixMilli `json:"ts"`
	Chunks []string       `json:"chunks"`
}

// pendingWrite is one in-flight Put's registry entry.
//
// It exists because an object is durable BEFORE its roots.jsonl line is: putObject renames the
// object into place, and a crash before appendRoot leaves content on disk that no index entry
// references. The sweep's freshness guard (an object younger than the pass is spared) covers that
// window only for the pass that is running; the marker covers it durably, across restarts, which
// is what SP-20 invariant 9's "pending write" clause requires.
type pendingWrite struct {
	s    *FSStore
	path string
}

// beginPendingWrite registers root's chunks as written-but-not-yet-rooted.
//
// A registry failure is NOT a Put failure. Every producer of a Put is a hook that §2.3 permits no
// exit code but 0, so an unwritable .qompack/state degrades to the sweep's freshness guard alone,
// counted as store.pending.degraded, rather than failing the session. The caller gets a nil
// *pendingWrite, whose done() is a no-op.
func (s *FSStore) beginPendingWrite(root core.Hash, refs []ChunkRef) (*pendingWrite, error) {
	dir := filepath.Join(s.l.State, pendingWriteDir)
	if err := os.MkdirAll(paths.Long(dir), 0o700); err != nil {
		return nil, err
	}
	name := make([]byte, pendingNameRandBytes)
	if _, err := rand.Read(name); err != nil {
		return nil, err
	}
	rec := pendingWire{V: pendingRecordVersion, Root: root.String(), TS: s.now()}
	for _, c := range refs {
		rec.Chunks = append(rec.Chunks, c.Hash.String())
	}
	b, err := json.Marshal(rec)
	if err != nil {
		return nil, err
	}
	p := filepath.Join(dir, hex.EncodeToString(name)+pendingWriteSuffix)
	if err := paths.WriteAtomic(p, append(b, '\n'), 0o600); err != nil {
		return nil, err
	}
	return &pendingWrite{s: s, path: p}, nil
}

// pending registers root's chunks, absorbing a registry failure into a counter.
func (s *FSStore) pending(root core.Hash, refs []ChunkRef) *pendingWrite {
	pw, err := s.beginPendingWrite(root, refs)
	if err != nil {
		s.count("store.pending.degraded", 1)
		s.log.Debug("store: could not register a pending write", "err", err)
		return nil
	}
	return pw
}

// done retires the marker once the root's index line has landed. A removal failure is harmless:
// the marker over-retains for one more GC pass and is expired by age after that.
func (p *pendingWrite) done() {
	if p == nil {
		return
	}
	_ = os.Remove(paths.Long(p.path))
}

// RetentionRootsPath is where AppendRetentionRoot writes, given a PROJECT root. It is exported so
// a producer that cannot import this package's internals can still name the file.
func RetentionRootsPath(projectRoot string) string {
	return filepath.Join(paths.Of(projectRoot).State, retentionRootsFile)
}

// AppendRetentionRoot declares one hash as a GC retention root, durably and append-only.
//
// This is the whole producer-side contract: a lease service, a rollback rehearsal or a backup
// manifest appends one line per hash it needs held, and removes nothing — a line is a claim that
// the object was needed at that moment, and GC treats the file as a set. A producer that owns its
// own journal (the daemon's delivery-lease file) need not call this at all; GC reads that journal
// directly.
func AppendRetentionRoot(projectRoot string, r RetentionRoot) error {
	if r.Hash.IsZero() {
		return fmt.Errorf("%w: a retention root needs a hash", core.ErrContract)
	}
	if r.Class == "" {
		return fmt.Errorf("%w: a retention root needs a class", core.ErrContract)
	}
	p := RetentionRootsPath(projectRoot)
	if err := os.MkdirAll(paths.Long(filepath.Dir(p)), 0o700); err != nil {
		return err
	}
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	w, err := paths.AppendOnly(p)
	if err != nil {
		return err
	}
	defer func() { _ = w.Close() }()
	_, err = w.Write(append(b, '\n'))
	return err
}

// ── delta read path ──────────────────────────────────────────────────────────────────────────

// ReadDelta returns one stored delta record and the durability of its declared base.
//
// The three failure outcomes are distinct and none of them reconstructs anything: an absent record
// or an absent/tombstoned base is FidelityUnavailable, an unreadable or unparseable payload is
// FidelityCorrupt. The returned DeltaRecord always carries whatever base the record declared, so a
// caller can name the missing base rather than reporting an anonymous failure.
func (s *FSStore) ReadDelta(ctx context.Context, deltaRoot core.Hash) (DeltaRecord, Fidelity, error) {
	if err := s.use(); err != nil {
		return DeltaRecord{}, FidelityUnavailable, err
	}
	if err := ctx.Err(); err != nil {
		return DeltaRecord{}, FidelityUnavailable, err
	}

	s.mu.RLock()
	entry, ok := s.rootIndex[deltaRoot]
	var base core.Hash
	if ok {
		base = entry.Base
	}
	_, baseLive := s.rootIndex[base]
	s.mu.RUnlock()
	if !ok {
		return DeltaRecord{Root: deltaRoot}, FidelityUnavailable,
			fmt.Errorf("%w: delta record %s", core.ErrNotFound, deltaRoot.Short())
	}

	rec := DeltaRecord{Root: deltaRoot, Base: base}
	payload, err := s.readRootBytes(ctx, deltaRoot)
	if err != nil {
		return rec, FidelityCorrupt, fmt.Errorf("%w: reading %s: %w", ErrDeltaCorrupt, deltaRoot.Short(), err)
	}
	deltas, err := unmarshalDeltas(payload)
	if err != nil {
		return rec, FidelityCorrupt, fmt.Errorf("%w: %s: %w", ErrDeltaCorrupt, deltaRoot.Short(), err)
	}
	rec.Deltas = deltas

	if base.IsZero() {
		return rec, FidelityUnavailable,
			fmt.Errorf("%w: delta record %s declares no base", ErrDeltaBaseUnavailable, deltaRoot.Short())
	}
	if !baseLive {
		return rec, FidelityUnavailable,
			fmt.Errorf("%w: base %s of delta %s", ErrDeltaBaseUnavailable, base.Short(), deltaRoot.Short())
	}
	return rec, FidelityExact, nil
}

// RestoreOriginal returns root's ORIGINAL, pre-canonicalization bytes and says exactly how good
// that answer is.
//
// It never guesses. A root with no recovery record answers FidelityCanonical with the canonical
// bytes — a caller that needs the original must read the status, not the length. A declared delta
// whose base or payload is gone answers FidelityUnavailable or FidelityCorrupt with NO bytes at
// all, because a partial reconstruction of an exactness claim is worse than an absent one.
func (s *FSStore) RestoreOriginal(ctx context.Context, root core.Hash) ([]byte, Fidelity, error) {
	if err := s.use(); err != nil {
		return nil, FidelityUnavailable, err
	}
	if err := ctx.Err(); err != nil {
		return nil, FidelityUnavailable, err
	}

	s.mu.RLock()
	entry, ok := s.rootIndex[root]
	var deltaRoot, orig core.Hash
	if ok {
		deltaRoot, orig = entry.Deltas, entry.Orig
	}
	s.mu.RUnlock()
	if !ok {
		return nil, FidelityUnavailable, fmt.Errorf("%w: root %s", core.ErrNotFound, root.Short())
	}

	// A retained full object is already the original; nothing has to be replayed onto it.
	if !orig.IsZero() {
		b, err := s.readRootBytes(ctx, orig)
		if err != nil {
			return nil, FidelityUnavailable,
				fmt.Errorf("%w: retained original %s of %s: %w", ErrDeltaBaseUnavailable, orig.Short(), root.Short(), err)
		}
		return b, FidelityFull, nil
	}

	canonical, err := s.readRootBytes(ctx, root)
	if err != nil {
		return nil, FidelityUnavailable, err
	}
	if deltaRoot.IsZero() {
		// No recovery record was ever stored. These are canonical bytes and the status says so.
		return canonical, FidelityCanonical, nil
	}

	rec, fid, err := s.ReadDelta(ctx, deltaRoot)
	if err != nil {
		return nil, fid, err
	}
	if rec.Base != root {
		return nil, FidelityCorrupt,
			fmt.Errorf("%w: delta %s declares base %s, not %s",
				ErrDeltaCorrupt, deltaRoot.Short(), rec.Base.Short(), root.Short())
	}
	out, err := canon.Restore(canonical, rec.Deltas)
	if err != nil {
		return nil, FidelityCorrupt, fmt.Errorf("%w: replaying %s: %w", ErrDeltaCorrupt, deltaRoot.Short(), err)
	}
	return out, FidelityExact, nil
}

// readRootBytes materializes one root's full content.
func (s *FSStore) readRootBytes(ctx context.Context, root core.Hash) ([]byte, error) {
	rc, err := s.Open(ctx, root)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rc.Close() }()
	return io.ReadAll(rc)
}

// deltaWire is the compact {"o","l","c","s"} shape marshalDeltas writes.
type deltaWire struct {
	O int    `json:"o"`
	L int    `json:"l"`
	C string `json:"c"`
	S string `json:"s"`
}

// unmarshalDeltas is marshalDeltas' inverse.
func unmarshalDeltas(b []byte) ([]canon.Delta, error) {
	var wire []deltaWire
	if err := json.Unmarshal(b, &wire); err != nil {
		return nil, err
	}
	out := make([]canon.Delta, 0, len(wire))
	for _, w := range wire {
		out = append(out, canon.Delta{Offset: w.O, Len: w.L, Original: w.S, Class: canon.Class(w.C)})
	}
	return out, nil
}

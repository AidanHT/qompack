package store

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
)

// Publication accounting (V6-RECOVERY-1): bounded PRODUCTION discovery of publication gaps that
// nothing on the daemon's startup path accounts for today. This is the store-side walk; the daemon
// (internal/daemon/publication_audit.go) turns one pass into a narrow report and a LOUD line.
//
// A gap is durable evidence with no publication, seen from the two ends of publication order
// (capture_sidecar.go header). The CRITERIA here are calibrated to `qompack fsck`'s own
// checkCaptures / checkObjects (internal/cli/fsck.go), so a startup announcement and an operator's
// fsck cannot disagree about what a gap is:
//
//   - An unpublished PUBLISHABLE capture. fsck's calibration rule 2: `published:false` ALONE is not a
//     gap — it is the state of every ordinary in-flight turn. A tool, prompt or subagent-stop
//     delivery with OutcomeOK and durable BytesHash requires a reference. A denied/absent/error
//     capture admits no bytes; ordinary Stop produces no derived record. Unknown stop provenance
//     remains incomplete.
//   - An unindexed OBJECT CANDIDATE. An object file no live index chunk references (F4-1). It is a
//     CANDIDATE, not a proven failed publication: a tombstoned-but-not-yet-GC-swept object is also
//     absent from the live index, and telling the two apart needs a tombstone-history replay this
//     bounded pass does not do. KeepRaw is NOT a source of these — putSideRecord routes the delta and
//     the retained-original through appendRoot, so their chunks are in chunkSet (put.go).
//
// Everything is ADDITIVE and READ-ONLY. The frozen Store / ReadOnlyStore interfaces (§5.8) are
// untouched; this is a concrete capability reached through the narrow PublicationAuditor interface,
// the RefCounter / SupersedingRecorder / Quarantiner pattern. It writes, repairs and deletes nothing.
//
// It is genuinely BOUNDED, and bounded is a claim the tests pin, not a comment:
//
//   - A shared scanBudget caps the TOTAL directory entries visited (files, subdirs, unknown entries
//     and symlinks alike) and the TOTAL bytes read across sidecars and pending markers. Directories
//     are read in fixed-size batches (os.File.ReadDir), never slurped whole, so a hostile directory of
//     millions of names cannot allocate before the count cap bites. Depth is fixed at the two fanout
//     levels — no recursive WalkDir.
//   - No object byte is ever read or re-hashed: an object's filename IS its content address, checked
//     against the in-memory index under a read lock (indexHasChunk).
//   - A sidecar is read only up to captureSidecarReadLimit — a hook capture's hard cap expanded for
//     base64, NOT store.MaxPutBytes — so one pathological file cannot turn the pass into a 64 MiB
//     read. A file past that bound is reported incomplete, never read and never called empty.
//   - ctx is checked at every batch and the pass stops on cancellation; main wraps a short startup
//     deadline around the call.
//   - A symlink or reparse point is NEVER traversed or read, so the walk cannot be lured into reading
//     arbitrary files outside .qompack.
//
// INCOMPLETE IS NOT EMPTY. A filesystem error, an unreadable record, an unknown/missing schema,
// version or outcome, a too-large file, a symlink, or any budget/deadline stop sets Incomplete and is
// NOTED; the gap counts become a lower bound. A zero gap count is "clean" ONLY when Incomplete is
// false. This is a snapshot of the live writer store taken after the startup drain and before serve;
// under a concurrent write it is preliminary, which is exactly why every unknown/error state stays
// explicit rather than being resolved into a confident number.

// The capture sidecar Op values this accounting classifies, spelled as literals because §3.2 does
// not give internal/store internal/ipc: these are ipc.OpObserveTool / ipc.OpObserveStop /
// ipc.OpObservePrompt's wire forms, which the daemon copies verbatim into CaptureSidecar.Op and
// internal/observer matches the same way (observer/identity.go opObserveTool/opObserveStop).
const (
	auditOpObserveTool   = "observe.tool"
	auditOpObserveStop   = "observe.stop"
	auditOpObservePrompt = "observe.prompt"
)

// The CONTROL ops a hook client sends with a delivery nonce besides the three observations:
// ipc.OpSessionStart / ipc.OpCheckpoint / ipc.OpFlush's wire forms, spelled as literals for the same
// reason as the three above. Only the hook client mints nonces, and these six are every op it sends.
const (
	auditOpSessionStart = "session.start"
	auditOpCheckpoint   = "checkpoint"
	auditOpFlush        = "flush"
)

// IsControlCaptureOp reports whether op names a control line (a session start, a checkpoint or a
// SessionEnd flush) rather than an observation.
//
// No current build writes a capture sidecar for one. Builds before the V6 close-out did: their drain
// published a sidecar for every LEASED line it replayed, and a control hook that had fallen back to
// its client spool reached the drain leased, because the hook client mints a nonce for every hook. A
// control line is not an observation and nothing ever references its sidecar, so such a file is a
// known legacy artifact: evidence of a delivery, kept exactly as written, and neither a publication
// gap nor damage. Every other op the accounting does not name stays unrecognized.
func IsControlCaptureOp(op string) bool {
	switch op {
	case auditOpSessionStart, auditOpCheckpoint, auditOpFlush:
		return true
	default:
		return false
	}
}

// publicationScanDefaults are applied to any non-positive cap field. They are a bound on WORK, not a
// configuration default (§11.6 D11): a project that has accumulated more than this in one unswept
// lifetime is exactly the case where a startup pass must stop and say it was truncated.
const (
	defaultMaxCaptureScan = 8192
	defaultMaxObjectScan  = 16384 //nomagic:allow bounded diagnostic work, not a configurable object budget.
	// defaultMaxEntries bounds the TOTAL directory entries any one pass visits. It is generous enough
	// for a healthy fanout (up to 256 capture shards, plus a bounded object tree) and small enough
	// that a directory bomb reports truncation instead of walking forever.
	defaultMaxEntries = 1 << 18
	// defaultMaxBytes bounds the TOTAL bytes read across sidecars and pending markers in one pass.
	defaultMaxBytes = 32 << 20
	// scanDirBatch is how many entries one os.File.ReadDir call returns. A directory is read in
	// batches so an arbitrarily large listing is never materialized whole before the count cap.
	scanDirBatch = 256
	// captureSidecarReadLimit bounds one sidecar read. A sidecar's `bytes` field holds at most a hook
	// capture's hard cap (config.HookCaptureHardCapBytes, 4 MiB), which encoding/json renders as
	// base64 (≈4/3 expansion) beside the small scalar fields — so a well-formed sidecar is at most
	// ~6 MiB. The bound is that with margin, deliberately NOT store.MaxPutBytes (64 MiB): reading a
	// whole Put payload per sidecar at startup is the unbounded work this pass must not do.
	captureSidecarReadLimit = config.HookCaptureHardCapBytes*2 + (64 << 10)
	// pendingMarkerReadLimit bounds one pending-write marker read. A marker is a root hash and its
	// chunk list; anything past 1 MiB is not a marker this store wrote.
	pendingMarkerReadLimit = 1 << 20
	// maxAuditNotes bounds PublicationAudit.Notes so a badly damaged or hostile tree cannot make the
	// reason list unbounded. The vocabulary is fixed strings; the bound guards the repeat count.
	maxAuditNotes = 24
)

// PublicationScanCap is the explicit bound one AuditPublication pass runs under. A non-positive field
// takes publicationScanDefaults; the daemon passes DefaultPublicationScanCap().
type PublicationScanCap struct {
	// MaxCaptures bounds how many capture sidecars are classified.
	MaxCaptures int
	// MaxObjects bounds how many object leaf files are examined.
	MaxObjects int
	// MaxEntries bounds the total directory entries visited across the whole pass (files, subdirs,
	// unknown entries and symlinks included).
	MaxEntries int
	// MaxBytes bounds the total bytes read across all sidecar and pending-marker reads.
	MaxBytes int64
}

// DefaultPublicationScanCap is the cap a caller with no reason to choose its own should pass.
func DefaultPublicationScanCap() PublicationScanCap {
	return PublicationScanCap{
		MaxCaptures: defaultMaxCaptureScan,
		MaxObjects:  defaultMaxObjectScan,
		MaxEntries:  defaultMaxEntries,
		MaxBytes:    defaultMaxBytes,
	}
}

// withDefaults fills any non-positive field from publicationScanDefaults.
func (c PublicationScanCap) withDefaults() PublicationScanCap {
	if c.MaxCaptures <= 0 {
		c.MaxCaptures = defaultMaxCaptureScan
	}
	if c.MaxObjects <= 0 {
		c.MaxObjects = defaultMaxObjectScan
	}
	if c.MaxEntries <= 0 {
		c.MaxEntries = defaultMaxEntries
	}
	if c.MaxBytes <= 0 {
		c.MaxBytes = defaultMaxBytes
	}
	return c
}

// PublicationAudit is one bounded pass's result. Every field is a count, a boolean or a
// fixed-vocabulary note: no observation id, path, hash or byte of content anywhere in it, so it is
// safe for a diagnostic to log verbatim.
type PublicationAudit struct {
	// CapturesScanned and ObjectsScanned are how many of each were examined. Under Truncated they are
	// the cap, not the tree's true size.
	CapturesScanned int
	ObjectsScanned  int

	// UnpublishedCaptures counts publishable tool, prompt or subagent-stop captures:
	// delivery whose Outcome is core.OutcomeOK and whose BytesHash is durable, still unpublished
	// (fsck calibration rule 2). This is the V6-RECOVERY-1 capture gap.
	UnpublishedCaptures int
	// LegitimatelyUnpublished is the count of sidecars that are unpublished for a GOOD reason, not a
	// gap: an ordinary Stop, a non-ok outcome (a denied/absent/error
	// capture admitted no bytes), or an ok capture that retained no durable bytes. Counted only so the
	// aggregate shows they were seen and correctly set aside, never silently promoted to a gap.
	LegitimatelyUnpublished int

	// UnindexedObjectCandidates is the count of object files no live index chunk references and no
	// pending-write marker covers. It is a CANDIDATE count: it includes both crash-orphans (F4-1) and
	// tombstoned-but-unswept objects, which this pass does not distinguish (that needs a tombstone
	// replay). Reported honestly, never repaired or deleted.
	UnindexedObjectCandidates int
	// PendingObjects is the count of unindexed objects a pending-write marker DOES cover — an in-flight
	// Put whose root line has not landed. Not a gap; drain/GC owns it.
	PendingObjects int

	// LegacyControlCaptures counts unpublished sidecars of a CONTROL line (IsControlCaptureOp): the
	// known artifact builds before the V6 close-out left for a drained session start, checkpoint or
	// flush. They are classified, so they do not make the pass incomplete; they are not gaps, because a
	// control line needs no reference; and they are counted so a consumer can say they were seen and
	// kept rather than silently skipped.
	LegacyControlCaptures int

	// NewerSchemaCaptures counts sidecars declaring a schema NEWER than this build. They are written by
	// a newer plugin — a support gap (Qompack.md §7.1), never damage — and are not classified, so they
	// make the pass Incomplete exactly like every other unreadable record. The count exists so a
	// consumer can tell that one cause apart from the rest (IncompleteOnlyForNewerSchemas).
	NewerSchemaCaptures int

	// Incomplete is true when this pass could NOT be exhaustive: a filesystem error, an unreadable or
	// unknown-schema/outcome record, a too-large or symlinked entry, or a budget/deadline stop. Under
	// Incomplete the gap counts are a LOWER BOUND and must never be read as "clean" when zero.
	Incomplete bool
	// Truncated is true specifically when a count, entry or byte budget stopped a walk before it
	// finished. It always implies Incomplete.
	Truncated bool
	// Notes are the distinct, generic causes of incompleteness, bounded by maxAuditNotes. Safe to log.
	Notes []string
}

// HasGaps reports whether this pass FOUND a genuine, actionable publication gap, independent of
// Incomplete: an unpublished publishable capture, or an unindexed object candidate (the F4 cuts).
func (a PublicationAudit) HasGaps() bool {
	return a.UnpublishedCaptures > 0 || a.UnindexedObjectCandidates > 0
}

// noteNewerCaptureSchema is the one incompleteness note that names a support gap rather than damage.
const noteNewerCaptureSchema = "capture sidecar schema newer than this build"

// IncompleteOnlyForNewerSchemas reports whether this pass is incomplete for exactly one reason:
// capture sidecars written by a newer build. The pass is still NOT certified — their fields are not
// this build's to classify, so the gap counts remain a lower bound — but the cause is a support gap
// (Qompack.md §7.1), which fsck's captures row already reports as "a support gap rather than damage".
// Any other cause beside it — a truncation, an older or unreadable record, an unknown op or outcome,
// an unexpected or symlinked entry, an unresolved observation intent, an interrupted scan — returns
// false. Every cause of incompleteness is recorded through note (deduplicated), so a single note that
// is this one means no other cause was seen; a notes list at its bound can never be length one.
func (a PublicationAudit) IncompleteOnlyForNewerSchemas() bool {
	return a.Incomplete && !a.Truncated && a.NewerSchemaCaptures > 0 &&
		len(a.Notes) == 1 && a.Notes[0] == noteNewerCaptureSchema
}

// note records a generic, non-sensitive cause of incompleteness and sets Incomplete. Duplicates are
// dropped and the list is bounded; the vocabulary is fixed strings, never caller or filesystem data.
func (a *PublicationAudit) note(reason string) {
	a.Incomplete = true
	for _, n := range a.Notes {
		if n == reason {
			return
		}
	}
	if len(a.Notes) >= maxAuditNotes {
		return
	}
	a.Notes = append(a.Notes, reason)
}

// PublicationAuditor is the narrow capability a consumer reaches AuditPublication through, so the
// daemon needs no bare type assertion on *FSStore and the §5.8 interfaces stay frozen.
type PublicationAuditor interface {
	AuditPublication(ctx context.Context, scanCap PublicationScanCap) (PublicationAudit, error)
}

var _ PublicationAuditor = (*FSStore)(nil)

// scanBudget is the shared work bound one pass runs under: total directory entries it may visit and
// total bytes it may read. It is threaded through every walk so no single directory or file can
// exceed the pass's overall ceiling.
type scanBudget struct {
	entriesLeft int
	bytesLeft   int64
}

// AuditPublication walks the capture sidecars and the object tree once, bounded by scanCap, and
// reports what has durable evidence but no publication. It writes nothing.
//
// It returns an error only for a store that cannot be asked at all — closed (core.ErrDegraded) — or a
// context cancelled by the time the pass finishes. A filesystem problem inside the tree becomes
// Incomplete with a note, because a walk that half-finished reports a lower bound rather than failing
// the daemon's startup. A read-only store (OpenReadOnly) is a valid receiver: this is a read, gated
// by use() not mutate(), so `qompack fsck` can share the same accounting.
func (s *FSStore) AuditPublication(ctx context.Context, scanCap PublicationScanCap) (PublicationAudit, error) {
	if err := s.use(); err != nil {
		return PublicationAudit{}, err
	}
	if err := ctx.Err(); err != nil {
		return PublicationAudit{}, err
	}
	scanCap = scanCap.withDefaults()
	bud := &scanBudget{entriesLeft: scanCap.MaxEntries, bytesLeft: scanCap.MaxBytes}

	var a PublicationAudit
	s.auditObservationBindings(ctx, bud, &a)
	s.auditCaptures(ctx, scanCap.MaxCaptures, bud, &a)
	pending := s.pendingObjectChunks(ctx, bud, &a)
	s.auditObjects(ctx, scanCap.MaxObjects, bud, &a, pending)

	if err := ctx.Err(); err != nil {
		a.note("scan interrupted before it finished")
		return a, err
	}
	return a, nil
}

// eachDirEntry opens dir and calls fn for each entry in bounded batches, charging the shared entry
// budget for every entry visited. It returns false when ctx or the entry budget is exhausted, or when
// fn returns false — the signal to the caller to stop this phase. A missing directory is a silent
// true; an unreadable directory, or a mid-read failure, is noted (incomplete) and returns true, so
// one bad subdirectory does not abort the whole pass.
func (s *FSStore) eachDirEntry(ctx context.Context, dir string, bud *scanBudget, a *PublicationAudit,
	fn func(os.DirEntry) bool,
) bool {
	f, err := s.openPublicationPath(dir)
	if err != nil {
		if !os.IsNotExist(err) {
			a.note("a directory under .qompack could not be opened")
		}
		return true
	}
	defer func() { _ = f.Close() }()

	for {
		if err := ctx.Err(); err != nil {
			a.note("scan interrupted before it finished")
			return false
		}
		batch, readErr := f.ReadDir(scanDirBatch)
		for _, e := range batch {
			if ctx.Err() != nil {
				a.note("scan interrupted before it finished")
				return false
			}
			if bud.entriesLeft <= 0 {
				a.Truncated = true
				a.note("scan reached its directory-entry budget")
				return false
			}
			bud.entriesLeft--
			if !fn(e) {
				return false
			}
		}
		if errors.Is(readErr, io.EOF) {
			return true
		}
		if readErr != nil {
			a.note("a directory under .qompack could not be read")
			return true
		}
	}
}

// isSymlinkish reports whether e is a symlink or reparse point, which the walk never traverses or
// reads — the guard against being lured into reading files outside .qompack.
func isSymlinkish(e os.DirEntry) bool {
	return e.Type()&(os.ModeSymlink|os.ModeIrregular) != 0
}

// isRealDir reports whether e is an ordinary directory (not a symlink/reparse point).
func isRealDir(e os.DirEntry) bool { return e.IsDir() && !isSymlinkish(e) }

// captureAuditView is the fields the accounting needs from a sidecar — the same subset fsck's
// fsckSidecarLine reads, minus the identity strings. Decoding into it rather than a full
// CaptureSidecar keeps the captured payload (Bytes) out of the classification.
type captureAuditView struct {
	Version   int                  `json:"v"`
	Op        string               `json:"op"`
	Published bool                 `json:"published"`
	Outcome   core.EvidenceOutcome `json:"outcome"`
	BytesHash core.Hash            `json:"bytes_hash"`
	Bytes     []byte               `json:"bytes,omitempty"`
}

// auditCaptures classifies every capture sidecar under records/captures/, bounded by maxCaptures and
// the shared budget. A missing tree is not a gap; an unreadable one is incomplete.
func (s *FSStore) auditCaptures(ctx context.Context, maxCaptures int, bud *scanBudget, a *PublicationAudit) {
	root := filepath.Join(s.l.Records, captureSidecarDir)
	s.eachDirEntry(ctx, root, bud, a, func(shard os.DirEntry) bool {
		if isSymlinkish(shard) {
			a.note("symlink or reparse point in the capture tree was not traversed")
			return true
		}
		if !shard.IsDir() {
			a.note("unexpected entry in the capture tree")
			return true
		}
		shardPath := filepath.Join(root, shard.Name())
		return s.eachDirEntry(ctx, shardPath, bud, a, func(file os.DirEntry) bool {
			if isSymlinkish(file) {
				a.note("symlink or reparse point in the capture tree was not traversed")
				return true
			}
			if file.IsDir() || !strings.HasSuffix(file.Name(), ".json") {
				return true
			}
			if a.CapturesScanned >= maxCaptures {
				a.Truncated = true
				a.note("capture scan reached its cap")
				return false
			}
			return s.classifyCaptureFile(filepath.Join(shardPath, file.Name()), file, bud, a)
		})
	})
}

// classifyCaptureFile reads and tallies one sidecar under the byte budget. It returns false only when
// the byte budget is exhausted (the signal to stop the phase). Every failure to read or parse is
// recorded as incomplete, never silently swallowed: a stage-one gap is exactly the state a torn
// record hides in.
func (s *FSStore) classifyCaptureFile(path string, entry os.DirEntry, bud *scanBudget, a *PublicationAudit) bool {
	a.CapturesScanned++
	info, err := entry.Info()
	if err != nil {
		a.note("capture sidecar unreadable")
		return true
	}
	if info.Size() > captureSidecarReadLimit {
		// Read past the limit is the unbounded work this pass forbids; the file is reported, not read.
		a.note("capture sidecar exceeds the read limit")
		return true
	}
	if info.Size() > bud.bytesLeft {
		a.Truncated = true
		a.note("scan reached its byte budget")
		return false
	}
	b, err := s.readPublicationFile(path, min(int64(captureSidecarReadLimit), bud.bytesLeft))
	bud.bytesLeft -= int64(len(b))
	if err != nil {
		a.note("capture sidecar unreadable")
		return true
	}
	var view captureAuditView
	// json.Unmarshal over the whole (bounded) buffer, so trailing garbage after a valid object is a
	// parse error rather than being silently ignored the way a streaming decoder would.
	if json.Unmarshal(b, &view) != nil {
		a.note("capture sidecar unreadable")
		return true
	}
	s.classifyCaptureView(view, a)
	return true
}

// classifyCaptureView applies fsck's own gap criterion to one sidecar's fields.
func (s *FSStore) classifyCaptureView(v captureAuditView, a *PublicationAudit) {
	if v.Version != CaptureSidecarVersion {
		// A newer schema is a support gap; an older/zero/missing one is an unreadable version. Both
		// mean this build cannot assert what Published/Outcome mean, so neither is classified.
		if v.Version > CaptureSidecarVersion {
			a.NewerSchemaCaptures++
			a.note(noteNewerCaptureSchema)
		} else {
			a.note("capture sidecar declares an unreadable version")
		}
		return
	}
	if v.Published {
		return
	}
	if IsControlCaptureOp(v.Op) {
		a.LegacyControlCaptures++
		return
	}
	if v.Op != auditOpObserveTool && v.Op != auditOpObservePrompt && v.Op != auditOpObserveStop {
		a.note("capture sidecar with an unrecognized op")
		return
	}
	if !isKnownOutcome(v.Outcome) {
		a.note("capture sidecar with an unrecognized or missing outcome")
		return
	}
	if v.Outcome != core.OutcomeOK || v.BytesHash.IsZero() {
		a.LegitimatelyUnpublished++
		return
	}
	required, known := CaptureRequiresReference(v.Op, v.Bytes)
	if !known {
		a.note("capture publication requirement is unknown")
		return
	}
	if required {
		a.UnpublishedCaptures++
	} else {
		a.LegitimatelyUnpublished++
	}
}

// isKnownOutcome reports whether o is one of core.EvidenceOutcome's closed set. An empty or future
// value is unknown, which the caller treats as incomplete rather than as a benign non-gap.
func isKnownOutcome(o core.EvidenceOutcome) bool {
	switch o {
	case core.OutcomeOK, core.OutcomeAbsent, core.OutcomeUnavailable, core.OutcomeDenied,
		core.OutcomeCorrupt, core.OutcomeExpired, core.OutcomeUncertain:
		return true
	default:
		return false
	}
}

// auditObjects counts object leaf files that no live index chunk references, bounded by maxObjects
// and the shared budget. The object filename IS the content address, so membership is a map lookup
// against the loaded chunk set — no object byte is read and nothing is re-hashed. Depth is fixed at
// the two fanout levels; a symlinked directory is never followed.
func (s *FSStore) auditObjects(ctx context.Context, maxObjects int, bud *scanBudget,
	a *PublicationAudit, pending map[core.Hash]struct{},
) {
	s.eachDirEntry(ctx, s.l.Objects, bud, a, func(l1 os.DirEntry) bool {
		if !isRealDir(l1) {
			noteStrayObject(a, l1)
			return true
		}
		return s.eachDirEntry(ctx, filepath.Join(s.l.Objects, l1.Name()), bud, a, func(l2 os.DirEntry) bool {
			if !isRealDir(l2) {
				noteStrayObject(a, l2)
				return true
			}
			leaf := filepath.Join(s.l.Objects, l1.Name(), l2.Name())
			return s.eachDirEntry(ctx, leaf, bud, a, func(f os.DirEntry) bool {
				return s.classifyObjectLeaf(f, maxObjects, pending, a)
			})
		})
	})
}

// classifyObjectLeaf tallies one object leaf file. It returns false only when the object cap is
// reached (the signal to stop the phase).
func (s *FSStore) classifyObjectLeaf(f os.DirEntry, maxObjects int,
	pending map[core.Hash]struct{}, a *PublicationAudit,
) bool {
	if isSymlinkish(f) {
		a.note("symlink or reparse point in the object tree was not traversed")
		return true
	}
	if f.IsDir() {
		a.note("unexpected directory in an object fanout")
		return true
	}
	if a.ObjectsScanned >= maxObjects {
		a.Truncated = true
		a.note("object scan reached its cap")
		return false
	}
	a.ObjectsScanned++
	h, ok := objectHashFromName(f.Name())
	if !ok {
		a.note("object file name is not a content address")
		return true
	}
	if s.indexHasChunk(h) {
		return true
	}
	if _, isPending := pending[h]; isPending {
		a.PendingObjects++
		return true
	}
	a.UnindexedObjectCandidates++
	return true
}

// noteStrayObject records a non-directory or symlinked entry where a fanout directory was expected.
func noteStrayObject(a *PublicationAudit, e os.DirEntry) {
	if isSymlinkish(e) {
		a.note("symlink or reparse point in the object tree was not traversed")
		return
	}
	a.note("unexpected entry in the object tree")
}

// indexHasChunk reports whether h is a live chunk of the loaded index, taken under the read lock so a
// concurrent index write cannot tear it.
func (s *FSStore) indexHasChunk(h core.Hash) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.chunkSet[h]
	return ok
}

// objectHashFromName parses a leaf filename back into the content address it encodes, stripping the
// optional compression suffix. A name that is not 64 lowercase hex characters is not an object this
// store wrote.
func objectHashFromName(name string) (core.Hash, bool) {
	hx := strings.TrimSuffix(name, objectSuffix)
	if len(hx) != 2*len(core.Hash{}) || hx != strings.ToLower(hx) {
		return core.Hash{}, false
	}
	h, err := core.ParseHash(hx)
	if err != nil {
		return core.Hash{}, false
	}
	return h, true
}

// pendingObjectChunks reads the in-flight pending-write markers and returns the chunk hashes they
// cover, bounded by the shared budget. A marker names an object whose root line has not landed yet
// (lifecycle.go pendingWrite), so an unindexed object it covers is a Put in progress, not a gap.
//
// An unreadable registry or a marker this build cannot parse is NOTED — the pass proceeds with the
// markers it could read, and any object a missing marker would have covered is reported as an
// unindexed candidate, the safe over-reporting direction.
func (s *FSStore) pendingObjectChunks(ctx context.Context, bud *scanBudget, a *PublicationAudit) map[core.Hash]struct{} {
	out := make(map[core.Hash]struct{})
	dir := filepath.Join(s.l.State, pendingWriteDir)
	s.eachDirEntry(ctx, dir, bud, a, func(e os.DirEntry) bool {
		if isSymlinkish(e) {
			a.note("symlink or reparse point in the pending registry was not traversed")
			return true
		}
		if e.IsDir() || !strings.HasSuffix(e.Name(), pendingWriteSuffix) {
			return true
		}
		info, err := e.Info()
		if err != nil {
			a.note("pending-write marker unreadable")
			return true
		}
		if info.Size() > pendingMarkerReadLimit {
			a.note("pending-write marker exceeds the read limit")
			return true
		}
		if info.Size() > bud.bytesLeft {
			a.Truncated = true
			a.note("scan reached its byte budget")
			return false
		}
		b, err := s.readPublicationFile(filepath.Join(dir, e.Name()), min(int64(pendingMarkerReadLimit), bud.bytesLeft))
		bud.bytesLeft -= int64(len(b))
		if err != nil {
			a.note("pending-write marker unreadable")
			return true
		}
		var rec pendingWire
		if json.Unmarshal(b, &rec) != nil {
			a.note("pending-write marker unparseable")
			return true
		}
		for _, cstr := range rec.Chunks {
			if h, herr := core.ParseHash(cstr); herr == nil {
				out[h] = struct{}{}
			}
		}
		return true
	})
	return out
}

// A directory handle confines opens even if a link changes after enumeration.
// The project root itself is the caller's trusted anchor.
func (s *FSStore) openPublicationPath(name string) (*os.File, error) {
	r, err := os.OpenRoot(s.root)
	if err != nil {
		return nil, err
	}
	defer func() { _ = r.Close() }()
	rel, err := filepath.Rel(s.root, name)
	if err != nil {
		return nil, err
	}
	for part := rel; part != "."; part = filepath.Dir(part) {
		info, err := r.Lstat(part)
		if err != nil {
			return nil, err
		}
		if info.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0 {
			return nil, core.ErrDegraded
		}
	}
	return r.Open(rel)
}

func (s *FSStore) readPublicationFile(name string, limit int64) ([]byte, error) {
	f, err := s.openPublicationPath(name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil, core.ErrDegraded
	}
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if int64(len(b)) > limit {
		return b, core.ErrBudget
	}
	return b, err
}

// CaptureRequiresReference describes the current observer contract. Ordinary Stop
// has no derived record; SubagentStop does. An unreadable retained event cannot
// establish that distinction. Old prompt records may predate reference capture;
// their missing link is unverified under today's contract, not repaired here. A
// control line (IsControlCaptureOp) is not an observation and never needs one.
func CaptureRequiresReference(op string, retained []byte) (required, known bool) {
	switch op {
	case auditOpObserveTool, auditOpObservePrompt:
		return true, true
	case auditOpSessionStart, auditOpCheckpoint, auditOpFlush:
		return false, true
	case auditOpObserveStop:
		var event struct {
			Name     string `json:"hook_event_name"`
			Subagent bool   `json:"subagent"`
		}
		if json.Unmarshal(retained, &event) != nil {
			return false, false
		}
		if event.Name == "SubagentStop" || event.Subagent {
			return true, true
		}
		if event.Name == "Stop" {
			return false, true
		}
	}
	return false, false
}

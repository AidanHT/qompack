package store

import (
	"context"
	"errors"
	"path/filepath"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/obs"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/tokens"
)

// The read-only store, added for `qompack fsck` and `qompack doctor` (SP-17 Task 5, ruling R5-A).
//
// `Open` is not usable by a diagnostic. It runs paths.EnsureLayout and ensureStoreDirs and then
// takes O_APPEND|O_CREATE handles on all five index files (open.go), so merely ASKING a project a
// question manufactures empty index files inside it. Measured on a directory holding only
// `.qompack/`, that was 26 new paths. An operator inspecting a project half-restored from a backup
// would have the missing index files created as empty, and the next integrity pass would then
// report a clean empty index rather than a missing one.
//
// OpenReadOnly is the same loader with every writing step removed: no layout creation, no append
// handles, a segment log replayed by the one opener that does not create its file, and a Close that
// does not Flush. It creates nothing and moves nothing, which is what makes "fsck is read-only" a
// checkable claim rather than a description.
//
// It is deliberately a NARROW interface rather than a Store. A Store carries Put, RecordToolUse,
// GC and Flush, and a read-only value behind that interface would be a value whose contract is a
// lie for most of its methods; returning only the read half means a caller cannot reach them at
// all. §5.8's Store interface is unchanged.
//
// "Cannot reach them" is enforced twice, because the first version enforced it nowhere. It embedded
// *FSStore, and an embedded pointer PROMOTES every method, so the returned value still satisfied
// store.Store and a one-line type assertion handed a caller the whole mutating half: measured
// through it, GC deleted an unreferenced object, PutBytes created five directories and two files
// before panicking on the nil roots writer, and Flush reported success. So:
//
//  1. readOnlyStore names its store in an UNEXPORTED FIELD and delegates the six read methods
//     explicitly. Nothing is promoted, the assertion to store.Store fails, and adding a method to
//     Store no longer silently widens this value.
//  2. Every mutating method on *FSStore calls mutate() (fsstore.go), which returns ErrReadOnly on
//     a read-only store. That is the guard for the store BEHIND the value — reached from inside
//     this package, or by anything that comes to hold the *FSStore later.
//
// Neither alone is enough: (1) can be undone by one future embedding, and (2) is a runtime refusal
// where a caller may have expected a write to happen.
//
// The segment log is the one sub-object with writers of its own, reachable through Segments()
// rather than through a method on the store, so it carries the same refusal twice over. Its primary
// guard is segLog.writable (segments.go), which every public writer consults BEFORE it validates
// anything, so a caller hears why the call cannot happen at all rather than what would have been
// wrong with it; segLog.append repeats the refusal at the single choke point every record passes
// through, as the last line of defence for a writer added later that forgets the first.

// ErrReadOnly is what every mutating method reports on a store opened by OpenReadOnly. It is a
// refusal, not a degradation: the call did nothing, and nothing about the store changed.
var ErrReadOnly = errors.New("store: the store is open read-only")

// ReadOnlyStore is the read half a diagnostic needs.
type ReadOnlyStore interface {
	// GetRoot returns root's description from the loaded index.
	GetRoot(ctx context.Context, root core.Hash) (Root, error)
	// GetChunk returns one chunk's plaintext, verifying its content address. A rejected object is
	// reported and LEFT WHERE IT IS: see quarantine's read-only branch.
	GetChunk(ctx context.Context, h core.Hash) ([]byte, error)
	// ReadDelta reads a delta side record and reports the fidelity that follows from it.
	ReadDelta(ctx context.Context, deltaRoot core.Hash) (DeltaRecord, Fidelity, error)
	// RestoreOriginal reports what can be recovered for root, and at what fidelity.
	RestoreOriginal(ctx context.Context, root core.Hash) ([]byte, Fidelity, error)
	// Stats summarizes what the loaded indices hold.
	Stats(ctx context.Context) (Stats, error)
	// Close releases the loaded indices. It never flushes: there is nothing to persist.
	Close() error
}

// readOnlyStore is the returned value: an FSStore loaded without any writer, held in an unexported
// field so that NOTHING is promoted and the value satisfies ReadOnlyStore and nothing wider.
type readOnlyStore struct{ fs *FSStore }

// The compile-time statement of both halves of the design: the value is a ReadOnlyStore, and the
// second line is why `var _ Store = readOnlyStore{}` is absent rather than merely untrue — see
// TestOpenReadOnly_TheValueIsNotAStore, which asserts the assertion fails.
var _ ReadOnlyStore = readOnlyStore{}

// AuditPublication is an optional read capability. It exposes diagnostics
// without widening the frozen ReadOnlyStore interface or granting mutations.
func (r readOnlyStore) AuditPublication(ctx context.Context, cap PublicationScanCap) (PublicationAudit, error) {
	return r.fs.AuditPublication(ctx, cap)
}

// LegacyPromptRecords is an optional read capability, like AuditPublication: the prompt records an
// earlier build's unlinked prompt sidecars may claim (legacy_prompt.go), for `qompack fsck`'s
// captures row to claim from under the same rule the audit applies.
func (r readOnlyStore) LegacyPromptRecords() map[LegacyPromptKey]int {
	return r.fs.LegacyPromptRecords()
}

var _ LegacyPromptCounter = readOnlyStore{}

// GetRoot resolves root against the loaded index.
func (r readOnlyStore) GetRoot(ctx context.Context, root core.Hash) (Root, error) {
	return r.fs.GetRoot(ctx, root)
}

// GetChunk returns h's verified plaintext. A rejected object is reported and left where it is.
func (r readOnlyStore) GetChunk(ctx context.Context, h core.Hash) ([]byte, error) {
	return r.fs.GetChunk(ctx, h)
}

// ReadDelta reads a delta side record and the fidelity that follows from it.
func (r readOnlyStore) ReadDelta(ctx context.Context, deltaRoot core.Hash) (DeltaRecord, Fidelity, error) {
	return r.fs.ReadDelta(ctx, deltaRoot)
}

// RestoreOriginal reports what can be recovered for root, and at what fidelity.
func (r readOnlyStore) RestoreOriginal(ctx context.Context, root core.Hash) ([]byte, Fidelity, error) {
	return r.fs.RestoreOriginal(ctx, root)
}

// Stats summarizes what the loaded indices hold.
func (r readOnlyStore) Stats(ctx context.Context) (Stats, error) { return r.fs.Stats(ctx) }

// Close releases the store without flushing. Flush would materialize index/files.json, persist the
// cumulative counters and append a session record — three writes, on a handle set that does not
// exist here.
//
// It degrades the segment log for the reason FSStore.Close does: Segments() keeps handing back a
// non-nil log after Close, and fsstore.go's contract is that every one of that log's methods then
// reports core.ErrDegraded. Without this, Range and Get answered a closed store's caller from
// memory. degrade nil-checks the append handle (segments.go), so degrading a log that never held
// one closes nothing and writes nothing — the read-only claim is untouched.
func (r readOnlyStore) Close() error {
	r.fs.closed.Store(true)
	r.fs.seg.degrade()
	return nil
}

// OpenReadOnly returns a read-only view of the store rooted at root (the PROJECT root).
//
// It never creates a file or a directory: a project that has never been used with Qompack loads as
// an empty store rather than being brought into existence, and every index file it cannot find is
// simply absent (scanIndexJSONL treats a missing file as an empty one).
func OpenReadOnly(root string, cfg config.Config, deps Deps) (ReadOnlyStore, error) {
	l := paths.Of(root)
	deps = defaultReadOnlyDeps(l, cfg, deps)

	s := &FSStore{
		root:      root,
		l:         l,
		cfg:       cfg,
		deps:      deps,
		log:       deps.Log,
		readOnly:  true,
		rootIndex: make(map[core.Hash]*rootEntry),
		chunkSet:  make(map[core.Hash]int32),
		refs:      make(map[core.Hash]uint32),
		byPath:    make(map[string][]core.Hash),
		toolUse:   make(map[core.ToolUseID]*ToolUseRecord),
		byPathTU:  make(map[string][]core.ToolUseID),
		fileHist:  make(map[string][]FileVersion),
		sessions:  make(map[core.SessionID]*sessionEntry),
	}

	// No append handles: openAppendFile opens with O_CREATE.
	for _, load := range []func() error{s.loadRoots, s.loadToolUse, s.loadFiles, s.loadSessions} {
		if err := load(); err != nil {
			return nil, err
		}
	}

	// The segment log IS loaded, through the one opener that does not create its file.
	//
	// The first version left it nil and the second installed a degraded one, and BOTH made
	// Stats.Segments a confident zero: segmentCount reads len(byID) and never consults degraded,
	// so `qompack doctor` printed "0 segment(s)" with status ok for a project whose
	// index/segments.jsonl held three open records — the exact class of answer the spool row was
	// corrected for. A diagnostic that can say "three" must not say "zero", and must not say
	// "unknown" either when the file is right there and fsck already parses it for its own row.
	seg, err := openSegLogReadOnly(filepath.Join(l.Index, segmentsFile), deps.Clock, deps.Log)
	if err != nil {
		return nil, err
	}
	s.seg = seg
	s.loadStoreState()
	return readOnlyStore{fs: s}, nil
}

// defaultReadOnlyDeps fills deps the way defaultDeps does, except for the two members that persist.
//
// Tokens is built with no calibration and no chunk-cache path, so Calibrate and Flush have nowhere
// to write; the ordinary NewExact is handed <state>/chunktokens.bin and would rewrite it on Close.
func defaultReadOnlyDeps(l paths.Layout, cfg config.Config, deps Deps) Deps {
	if deps.Log == nil {
		deps.Log = logging.Nop()
	}
	if deps.Clock == nil {
		deps.Clock = core.SystemClock()
	}
	if deps.Metrics == nil {
		deps.Metrics = obs.New(deps.Clock)
	}
	if deps.Tokens == nil {
		deps.Tokens = tokens.New(cfg, "")
	}
	return defaultDeps(l, cfg, deps)
}

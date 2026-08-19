package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/qompack/qompack/internal/chunk"
	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/sketch"
)

// MaxPutBytes bounds a single Put/PutBytes call. A larger input is truncated — Truncated is
// reported on PutResult — rather than rejected, because every producer of a Put is a hook that
// §2.3 permits no exit code but 0 (00-ARCHITECTURE.md §5.8).
const MaxPutBytes = 64 << 20

// ErrSegmentOpen reports that a SegmentLog operation requiring a closed segment was given an open
// one. It is additive: SP-06 owns this package and no §5.8 signature changes.
var ErrSegmentOpen = errors.New("qompack: segment not closed")

// ErrSegmentExists reports that a caller asked SegmentLog.Open to allocate a segment ID that has
// already been handed out.
var ErrSegmentExists = errors.New("qompack: segment id already allocated")

// scannerInitialBuf and scannerMaxBuf size every bufio.Scanner this package uses to replay an
// append-only index. The 4 MiB ceiling is what lets a single roots.jsonl line carrying a very
// large chunk list load rather than fail with bufio.ErrTooLong.
const (
	scannerInitialBuf = 64 << 10
	scannerMaxBuf     = 4 << 20
)

// indexRecordVersion is the "v" every index line this package writes carries. It is a wire format,
// not a tunable.
const indexRecordVersion = 1

// storeKey is the canonical map key for a path anywhere in this package.
//
// paths.Key alone only case-folds; it does not convert separators or clean a leading "./". Both
// matter here, because ChangedSince receives depends_on paths written by other subplans and by
// users, and a dep that names the same file in a different spelling must still match its recorded
// version (§8.3: staleness is "a hash comparison it performs anyway", which requires the two
// sides to agree on identity first). paths.Norm is what canonicalizes fully, but it needs the
// project root and touches the filesystem, so it is not usable on a lookup path.
func storeKey(p string) string {
	if p == "" {
		return ""
	}
	q := path.Clean(filepath.ToSlash(p))
	q = strings.TrimPrefix(q, "./")
	if q == "." {
		return ""
	}
	return paths.Key(q)
}

// marshalLine encodes v as one compact, newline-terminated JSONL record with HTML escaping
// disabled, so a "<" inside a path or a preview survives verbatim.
func marshalLine(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// RefCounter exposes the approximate refcount 00-ARCHITECTURE.md §5.8's GC semantics maintain
// "purely for /qompack:status and for cheap is-this-worth-keeping decisions". It is a narrow
// interface so SP-14 can reach ApproxRefs without a bare type assertion on *FSStore.
type RefCounter interface {
	ApproxRefs(h core.Hash) uint32
}

// appendFile is one open append-only index file.
//
// Each index file carries its own mutex, held only across the Write itself, so a large object
// write never blocks an index append and two different indices never contend (the daemon's worker
// pool depends on this).
//
// paths.AppendOnly returns an io.WriteCloser, not an *os.File, so sync() asserts for the narrow
// syncer interface and silently skips when the handle does not implement it: a buffering wrapper
// is still correct, merely not fsynced, and that must never be the reason SessionEnd fails.
type appendFile struct {
	mu sync.Mutex
	p  string
	w  io.WriteCloser
}

// syncer is the Sync half of *os.File, asserted for rather than required.
type syncer interface{ Sync() error }

// openAppendFile opens p for append, creating it if absent.
func openAppendFile(p string) (*appendFile, error) {
	w, err := paths.AppendOnly(p)
	if err != nil {
		return nil, err
	}
	return &appendFile{p: p, w: w}, nil
}

// write appends one already-newline-terminated record.
func (a *appendFile) write(b []byte) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.w == nil {
		return core.ErrDegraded
	}
	_, err := a.w.Write(b)
	return err
}

// sync flushes the handle to disk when the handle supports it.
func (a *appendFile) sync() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.w == nil {
		return nil
	}
	if s, ok := a.w.(syncer); ok {
		return s.Sync()
	}
	return nil
}

// close releases the handle.
func (a *appendFile) close() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.w == nil {
		return nil
	}
	err := a.w.Close()
	a.w = nil
	return err
}

// rootEntry is one loaded index/roots.jsonl line: the Root itself plus the attribution and
// near-dup metadata the line carries alongside it.
type rootEntry struct {
	Root   Root
	TS     core.UnixMilli
	Tool   string
	Path   string
	Class  uint8
	Eph    bool
	Sig    sketch.Signature
	Deltas core.Hash
}

// sessionEntry is one loaded index/sessions.jsonl record. It is what makes GC's "10 sessions"
// retention axis computable (00-ARCHITECTURE.md §5.8).
type sessionEntry struct {
	ID       core.SessionID
	Start    core.UnixMilli
	End      core.UnixMilli
	Turns    int
	ToolUses int
	Roots    int
	Objects  int
	Bytes    int64
	RawBytes int64
	Dedup    float64
	// dirty marks a session whose counters changed since its last persisted record, so Flush
	// appends only what actually moved (TestFlush_Idempotent).
	dirty bool
}

// FSStore is the filesystem-backed Store: content objects under <root>/.qompack/objects with
// two-level sha256 fanout, and append-only indices under <root>/.qompack/index
// (00-ARCHITECTURE.md §5.8, Qompack.md §7.4, §8.2).
//
// It is safe for concurrent use by multiple goroutines. One RWMutex guards every in-memory index;
// each append-only file has its own mutex; object writes take no lock at all, being
// content-addressed with an atomic rename.
type FSStore struct {
	root string
	l    paths.Layout
	cfg  config.Config
	deps Deps
	log  logging.Logger

	// mu guards every in-memory index below.
	mu sync.RWMutex

	// ── roots.jsonl ──
	rootIndex map[core.Hash]*rootEntry
	// chunkSet maps every live chunk hash to the length roots.jsonl recorded for it. It is a
	// hash→length map rather than a set precisely so GetChunk can cross-check a decoded object
	// against its recorded size in O(1): the roots index is keyed by ROOT, so recovering that
	// length from a bare chunk hash any other way means scanning every root's chunk list —
	// O(roots × chunks) on a read budgeted at 60 µs.
	chunkSet map[core.Hash]int32
	refs     map[core.Hash]uint32
	byPath   map[string][]core.Hash

	// ── tool_use.jsonl ──
	toolUse  map[core.ToolUseID]*ToolUseRecord
	byPathTU map[string][]core.ToolUseID

	// ── files.jsonl ──
	fileHist map[string][]FileVersion

	// ── sessions.jsonl ──
	sessions map[core.SessionID]*sessionEntry

	// ── accounting (Stats) ──
	bytesOnDisk int64
	rawBytes    int64
	// filesDirty marks index/files.json as needing regeneration on the next Flush.
	filesDirty bool
	// statsDirty marks state/store.json as needing rewriting on the next Flush.
	statsDirty bool

	rootsW *appendFile
	tuW    *appendFile
	filesW *appendFile

	sessionsW *appendFile

	seg *segLog

	closeOnce sync.Once
	closed    atomic.Bool
}

// use is the closed-store guard. Every method with an error return calls it first and reports
// core.ErrDegraded on a closed store; Has (which returns bool) and Segments (which returns a
// non-nil log whose own methods degrade) are the two documented exceptions, because changing
// their signatures would be a §5.8 amendment.
func (s *FSStore) use() error {
	if s.closed.Load() {
		return core.ErrDegraded
	}
	return nil
}

// now returns the current time in milliseconds through the injected clock, so a FakeClock test
// sees deterministic timestamps.
func (s *FSStore) now() core.UnixMilli {
	return core.UnixMilli(s.deps.Clock.Now().UnixMilli())
}

// count increments a named counter when metrics are wired, and does nothing when they are not.
func (s *FSStore) count(name string, n int64) {
	if s.deps.Metrics == nil {
		return
	}
	if c := s.deps.Metrics.Counter(name); c != nil {
		c.Add(n)
	}
}

// splitChecked runs the injected chunker and guarantees the result actually tiles data.
//
// This is a data-loss guard, not a policy: Open(root) must reproduce exactly the bytes that were
// put, so a chunker that returns no chunks for non-empty input, or chunks that do not tile
// [0, len(data)) contiguously from zero, cannot be allowed to silently truncate content. Either
// failure falls back to a single chunk covering the whole input and is reported.
//
// The guard outlives the reason it was written for. It went in while internal/chunk was an SP-01
// stub whose Split returned nil, where a store that quietly wrote nothing would have looked like
// it worked; SP-04's real chunker tiles correctly, so the fallback is no longer routinely taken.
// It stays because Chunker is an INJECTED interface (§5.8) — the store cannot verify what it is
// handed, only what came back — and because "the chunks did not tile the input" is silent data
// loss, the one failure mode that must never be discovered from a short read months later.
// TestPutBytes_ChunkerDegradedGuard keeps it exercised with an explicit silentChunker.
func (s *FSStore) splitChecked(data []byte) []chunk.Chunk {
	if len(data) == 0 {
		return nil
	}
	chunks := s.deps.Chunker.Split(data)
	if chunkingCovers(chunks, len(data)) {
		return chunks
	}
	s.count("store.chunker.degraded", 1)
	s.log.Warn("store: chunker did not tile the input; storing it as a single chunk",
		"bytes", len(data), "chunks", len(chunks))
	return []chunk.Chunk{{
		Offset: 0,
		Len:    len(data),
		Hash:   core.HashBytes(core.DomainChunk, data),
	}}
}

// chunkingCovers reports whether chunks tile [0, n) contiguously, in order, with no gap or
// overlap and no zero-length member.
func chunkingCovers(chunks []chunk.Chunk, n int) bool {
	if len(chunks) == 0 {
		return false
	}
	var off int64
	for _, c := range chunks {
		if c.Len <= 0 || c.Offset != off {
			return false
		}
		off += int64(c.Len)
	}
	return off == int64(n)
}

// Segments returns this store's SegmentLog. It is never nil, so callers do not nil-check it, and
// after Close every one of its methods reports core.ErrDegraded.
func (s *FSStore) Segments() SegmentLog { return s.seg }

// ApproxRefs is the approximate refcount of 00-ARCHITECTURE.md §5.8's GC semantics. It is
// maintained alongside the authoritative mark-and-sweep purely for /qompack:status and for cheap
// "is this worth keeping" decisions — it is never what GC collects from.
func (s *FSStore) ApproxRefs(h core.Hash) uint32 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.refs[h]
}

// Close flushes, then releases every resource the store holds. It is idempotent: a second Close
// reports nil rather than an error.
func (s *FSStore) Close() error {
	var err error
	s.closeOnce.Do(func() { err = s.closeBody() })
	return err
}

// releaseWriters closes every append-only handle the store has opened, without flushing.
//
// It is openFS's failure path, not Close's: a store that failed to open is never returned, so no
// caller can ever close it, and anything it had already opened would otherwise be held until the
// process exits. Errors are discarded deliberately — the open error is the one worth reporting, and
// a close error on a handle nobody will use adds nothing. Flush is deliberately NOT called: the
// store never became usable, so there is nothing of the caller's to persist.
func (s *FSStore) releaseWriters() {
	if s.seg != nil {
		s.seg.degrade() // degrade closes the segment log's own handle
	}
	for _, a := range []*appendFile{s.rootsW, s.tuW, s.filesW, s.sessionsW} {
		if a != nil {
			_ = a.close()
		}
	}
	if c, ok := s.deps.Tokens.(io.Closer); ok {
		_ = c.Close()
	}
}

// closeBody is Close's body, run exactly once.
func (s *FSStore) closeBody() error {
	// Flush before the closed flag goes up, since Flush itself refuses a closed store.
	err := s.Flush(context.Background())
	s.closed.Store(true)

	if s.seg != nil {
		s.seg.degrade()
	}

	for _, a := range []*appendFile{s.rootsW, s.tuW, s.filesW, s.sessionsW} {
		if a == nil {
			continue
		}
		if cerr := a.close(); cerr != nil && err == nil {
			err = cerr
		}
	}
	if c, ok := s.deps.Tokens.(io.Closer); ok {
		if cerr := c.Close(); cerr != nil && err == nil {
			err = cerr
		}
	}
	return err
}

// quarantineDir is the subdirectory of .qompack/tmp a corrupt object is moved to. It is the one
// directory the store adds beyond paths.EnsureLayout's own tree, because it is store-specific.
const quarantineDir = "quarantine"

// ensureStoreDirs creates the store-specific directories paths.EnsureLayout does not own.
func ensureStoreDirs(l paths.Layout) error {
	return os.MkdirAll(paths.Long(filepath.Join(l.Tmp, quarantineDir)), 0o700)
}

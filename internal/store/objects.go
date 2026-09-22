package store

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"syscall"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
)

// objectSuffix is appended to every compressed object's filename. A store configured with
// store.compression = "none" writes the bare name instead, and readers try this suffix first and
// the bare name second, so a store whose compression setting changed mid-life still reads
// (Qompack.md §7.4).
const objectSuffix = ".zst"

// compressionNone is the store.compression value that turns object compression off.
const compressionNone = "none"

// fanoutWidth is how many hex characters each of the two fanout directory levels takes from the
// object's hash: objects/ab/cd/<64 hex> (Qompack.md §7.4 "sha256, 2-level fanout").
const fanoutWidth = 2

// objectTmpPrefix names the staging file every object write lands in before its atomic rename, so
// crash debris under .qompack/tmp is recognizable.
const objectTmpPrefix = "obj-"

// objectTmpRandBytes is how many random bytes the staging filename carries (rendered as 12 hex
// characters), which is what keeps two concurrent writers of the same chunk off each other's
// staging file.
const objectTmpRandBytes = 6

// renameRetries is how many times a rename is retried on Windows before it is reported as a
// failure. Antivirus and search indexers hold brief handles on freshly written files, which
// surfaces as ERROR_ACCESS_DENIED or ERROR_SHARING_VIOLATION on the rename rather than on the
// write. There is no companion backoff constant: see renameObject for why §6.1 forbids one.
const renameRetries = 3

// Windows system error numbers the object rename retries. They are named here rather than pulled
// from golang.org/x/sys/windows so this file needs no build tag and no new dependency; both
// lookups are gated on runtime.GOOS == "windows", so their POSIX meanings never apply.
const (
	winErrAccessDenied     = syscall.Errno(5)
	winErrSharingViolation = syscall.Errno(32)
)

// hexOf renders h as its 64 lowercase hex characters, without the "sha256:" prefix core.Hash's
// String carries. This is the object's filename, and the two fanout directories are its first
// four characters.
func hexOf(h core.Hash) string { return hex.EncodeToString(h[:]) }

// compressing reports whether this store compresses objects.
func (s *FSStore) compressing() bool { return s.cfg.Store.Compression != compressionNone }

// objectDir returns the two-level fanout directory an object with hex name hx lives in.
func (s *FSStore) objectDir(hx string) string {
	return filepath.Join(s.l.Objects, hx[:fanoutWidth], hx[fanoutWidth:fanoutWidth*2])
}

// objectPath returns the path this store WRITES h to, honouring store.compression.
func (s *FSStore) objectPath(h core.Hash) string {
	hx := hexOf(h)
	name := hx
	if s.compressing() {
		name += objectSuffix
	}
	return filepath.Join(s.objectDir(hx), name)
}

// objectCandidates returns the paths a reader tries for h, in order: the compressed name first,
// then the bare one. Trying both is what lets a store keep reading objects written before
// store.compression changed.
func (s *FSStore) objectCandidates(h core.Hash) [2]string {
	hx := hexOf(h)
	dir := s.objectDir(hx)
	return [2]string{filepath.Join(dir, hx+objectSuffix), filepath.Join(dir, hx)}
}

// objectExists reports whether any candidate file for h is present on disk.
func (s *FSStore) objectExists(h core.Hash) bool {
	for _, p := range s.objectCandidates(h) {
		if _, err := os.Stat(paths.Long(p)); err == nil {
			return true
		}
	}
	return false
}

// objectWritten reports whether h is present under the name THIS store would write it as.
//
// putObject uses this rather than objectExists: a writer only needs to know whether it would
// overwrite its own target, and checking the second candidate name costs an extra stat syscall on
// every novel chunk. Readers still try both names — that is what makes a store whose
// store.compression changed mid-life readable — but writers do not need to.
func (s *FSStore) objectWritten(h core.Hash) bool {
	_, err := os.Stat(paths.Long(s.objectPath(h)))
	return err == nil
}

// knownDirs remembers fanout directories already created in this process.
//
// The two-level fanout means up to 65 536 directories, but a session touches few, and re-issuing
// MkdirAll for each of them on every object write is a syscall the store pays hundreds of times
// per tool result. The cache is only ever an optimistic hint: a write whose directory turns out to
// be missing anyway falls back to creating it and retrying, so an entry that goes stale (a deleted
// project, a test's temp directory) costs one failed create rather than a lost object.
var knownDirs sync.Map

// ensureDir creates dir unless this process already created it.
func ensureDir(dir string) error {
	if _, ok := knownDirs.Load(dir); ok {
		return nil
	}
	if err := os.MkdirAll(paths.Long(dir), 0o700); err != nil {
		return err
	}
	knownDirs.Store(dir, struct{}{})
	return nil
}

// tmpObjectPath mints a fresh staging path under .qompack/tmp for one object write.
func (s *FSStore) tmpObjectPath() (string, error) {
	var b [objectTmpRandBytes]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("store: minting an object staging name: %w", err)
	}
	return filepath.Join(s.l.Tmp, objectTmpPrefix+hex.EncodeToString(b[:])), nil
}

// isRenameContention reports whether err is one of the two transient Windows failures a freshly
// written file's rename hits when an antivirus or indexer still holds a handle on it.
func isRenameContention(err error) bool {
	if runtime.GOOS != "windows" || err == nil {
		return false
	}
	var errno syscall.Errno
	if errors.As(err, &errno) {
		return errno == winErrAccessDenied || errno == winErrSharingViolation
	}
	return errors.Is(err, os.ErrPermission)
}

// renameObject moves the staging file onto its final content-addressed path.
//
// A rename onto an existing object is SUCCESS, not an error: objects are content-addressed, so a
// concurrent writer that got there first wrote byte-identical content and there is nothing to
// reconcile. On Windows the rename is retried a few times, because antivirus contention makes the
// first attempt spuriously fail often enough to matter under the daemon's worker pool.
//
// There is deliberately NO wall-clock backoff between attempts. 00-ARCHITECTURE.md §6.1 bans
// wall-clock sleeps outright — devtool lint's sleepcheck enforces it with no annotation escape
// hatch, and core.Clock exposes only Now/Since, so there is nothing legitimate to sleep on. The
// ban is also right on its own terms here: this runs inside budget B-C (l0_process p99 < 50 ms)
// and a 1/2/4 ms backoff would spend 7 ms of it waiting. runtime.Gosched yields the processor so a
// contending scanner can make progress without this goroutine burning either the CPU or the clock.
//
// A lock that outlives the retries fails the Put rather than being waited out. That is the correct
// direction: the caller degrades (§12.3), the staging file is cleaned up, and the identical object
// will be written again on the next attempt, because the content address has not changed.
func renameObject(tmp, dst string) error {
	var err error
	for attempt := 0; attempt <= renameRetries; attempt++ {
		if err = os.Rename(paths.Long(tmp), paths.Long(dst)); err == nil {
			return nil
		}
		// Another writer may have landed the identical object in the meantime.
		if _, statErr := os.Stat(paths.Long(dst)); statErr == nil {
			return nil
		}
		if !isRenameContention(err) {
			return err
		}
		runtime.Gosched()
	}
	return err
}

// putObject writes plain as the content-addressed object for h.
//
// It returns the number of COMPRESSED bytes written and whether the object was novel. An object
// that is already present returns (0, false, nil) — zero, not the existing file's size, because
// the only caller adds this to Stats.Bytes and an already-counted object must never be counted
// twice.
//
// A failed object write fails the enclosing Put: a root whose chunks are not all present on disk
// would be an unreadable lie, and reporting success for it would put an unrecoverable reference
// into index/roots.jsonl.
func (s *FSStore) putObject(h core.Hash, plain []byte) (int64, bool, error) {
	if s.objectWritten(h) {
		return 0, false, nil
	}

	payload := plain
	if s.compressing() {
		enc, err := Encode(plain)
		if err != nil {
			return 0, false, fmt.Errorf("store: compressing chunk %s: %w", h.Short(), err)
		}
		payload = enc
	}

	dst := s.objectPath(h)
	if err := ensureDir(filepath.Dir(dst)); err != nil {
		return 0, false, fmt.Errorf("store: creating the fanout directory for %s: %w", h.Short(), err)
	}

	tmp, err := s.tmpObjectPath()
	if err != nil {
		return 0, false, err
	}
	if err := s.writeStaged(tmp, payload); err != nil {
		return 0, false, fmt.Errorf("store: staging chunk %s: %w", h.Short(), err)
	}
	if err := renameObject(tmp, dst); err != nil {
		_ = os.Remove(paths.Long(tmp))
		return 0, false, fmt.Errorf("store: publishing chunk %s: %w", h.Short(), err)
	}
	return int64(len(payload)), true, nil
}

// writeStaged writes payload to tmp and closes it.
//
// This legacy path does not sync the object. Flush syncs indices, not these closed files, so
// successful publication here does not establish crash durability. SP-20's durable publication
// barrier must cover the object before committing a reference/frontier. Readers verify the
// bytes they find; Has is an optimistic presence hint, not an integrity or durability witness.
func (s *FSStore) writeStaged(tmp string, payload []byte) error {
	f, err := paths.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		// .qompack/tmp is created by EnsureLayout at Open, so the common path needs no MkdirAll at
		// all. Create it only when it has actually gone missing, and retry once.
		if mkErr := os.MkdirAll(paths.Long(s.l.Tmp), 0o700); mkErr != nil {
			return err
		}
		if f, err = paths.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600); err != nil {
			return err
		}
	}
	if _, err := f.Write(payload); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// readObjectFile reads h's raw on-disk bytes, reporting which candidate path it found and whether
// that path is compressed.
func (s *FSStore) readObjectFile(h core.Hash) (raw []byte, path string, compressed bool, err error) {
	cands := s.objectCandidates(h)
	for i, p := range cands {
		limit := int64(MaxPutBytes)
		if i == 0 {
			limit = encodedObjectLimit()
		}
		b, readErr := readBoundedObject(p, limit)
		if readErr == nil {
			return b, p, i == 0, nil
		}
		if errors.Is(readErr, errObjectTooLarge) {
			s.quarantine(h, p, "physical size exceeds limit")
			return nil, p, i == 0, fmt.Errorf("%w: object %s exceeds size limit", ErrDamaged, h.Short())
		}
		if !os.IsNotExist(readErr) {
			// The file is there and unreadable — a nonregular leaf, a leaf replaced mid-open, an I/O
			// refusal. That is not an integrity failure: the bytes were not checked. ErrDamaged still
			// maps the retrieval layer to unavailable (the right outcome). The wrap says the read
			// itself failed so a log line does not claim a failed checksum. The model-facing reason
			// string lives in mcp.damaged, which this package does not own.
			return nil, p, i == 0, fmt.Errorf("%w: reading object %s", ErrDamaged, h.Short())
		}
	}
	// No candidate file exists. Whether that is ABSENCE or DAMAGE is a question only the index can
	// answer, and answering it is the rest of finding S-3: a quarantined object is gone from
	// objects/ by construction — quarantine MOVED it — while its index entry survives, so a store
	// that reported "not found" for it told the retrieval layer the content was never there when in
	// fact it was refused and preserved as evidence. An index entry with no file is exactly the
	// shape §12.3 calls a corrupt object after the quarantine has happened.
	//
	// A hash the index does not know is an ordinary miss: nothing ever promised it.
	s.mu.RLock()
	_, indexed := s.chunkSet[h]
	s.mu.RUnlock()
	if indexed {
		return nil, "", false, fmt.Errorf(
			"%w: object %s is indexed but absent from objects/", ErrDamaged, h.Short())
	}
	return nil, "", false, fmt.Errorf("%w: object %s", core.ErrNotFound, h.Short())
}

var errObjectTooLarge = errors.New("store: physical object exceeds size limit")

// errObjectNotRegular and errObjectChanged are readBoundedObject's two leaf refusals: the path names
// something other than a regular file, or the file opened is not the one the leaf check examined.
// readObjectFile maps both to ErrDamaged without quarantining, because the bytes were never checked.
var (
	errObjectNotRegular = errors.New("store: object is not a regular file")
	errObjectChanged    = errors.New("store: object changed while opening")
)

// readBoundedObject rejects nonregular leaf paths and bounds allocation by the size the file's own
// Stat reports, which is already checked against limit. The leaf check is openObjectLeaf's, per
// platform: it never follows a symbolic link or reparse point at the leaf, and the file it returns
// is the one it checked (object_open_*.go). The plaintext hash check handles changed bytes. This is
// not a complete authorization check for ancestor directories.
//
// The bytes are read into ONE buffer pre-sized from the Stat'd size (W1: no io.ReadAll geometric
// regrowth, no transient over-allocation), and the read then requires the file to be exactly that
// long — a short read (the file shrank under us) and a trailing byte (it grew under us) are both
// refused. The trailing-byte probe is load-bearing, not tidiness: without it a file appended to
// after its Stat would be accepted on the strength of its valid PREFIX's content hash, since the
// downstream hash would only ever see the prefix this function returned.
func readBoundedObject(path string, limit int64) ([]byte, error) {
	f, opened, err := openObjectLeaf(paths.Long(path))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if opened.Size() > limit {
		return nil, errObjectTooLarge
	}
	return readExactSize(f, opened.Size())
}

// openObjectChecked is the path-verified object open: an Lstat refuses a nonregular leaf before
// anything is opened, and the opened handle's own Stat must be a regular file that os.SameFile
// matches with that Lstat, which detects a leaf replaced between the two. flags are added to
// O_RDONLY; object_open_unix.go passes the no-follow flags that close the replacement window
// instead of only detecting it.
//
// It is the whole leaf check where the platform offers nothing cheaper, and on Windows it is the
// fallback for a leaf that is a reparse point (object_open_windows.go).
func openObjectChecked(long string, flags int) (*os.File, os.FileInfo, error) {
	before, err := os.Lstat(long)
	if err != nil {
		return nil, nil, err
	}
	if !before.Mode().IsRegular() {
		return nil, nil, errObjectNotRegular
	}
	f, err := os.OpenFile(long, os.O_RDONLY|flags, 0)
	if err != nil {
		return nil, nil, err
	}
	opened, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, nil, err
	}
	if !opened.Mode().IsRegular() || !os.SameFile(before, opened) {
		_ = f.Close()
		return nil, nil, errObjectChanged
	}
	return f, opened, nil
}

// readExactSize reads exactly size bytes from r into a single pre-sized buffer, then requires r to be
// at EOF. It is separated from readBoundedObject so its truncation and growth handling can be tested
// deterministically against a crafted reader, without racing a real file.
//
// size is the caller's already-bounds-checked length (readBoundedObject rejects opened.Size() > limit
// first), so the one allocation is bounded by the validated limit. A read shorter than size is a
// shrink; a byte past size is a growth or appended suffix. Both are refused — the growth refusal is
// what stops a valid-prefix hash from certifying an appended object (see readBoundedObject).
func readExactSize(r io.Reader, size int64) ([]byte, error) {
	if size == 0 {
		// A legitimate zero-byte object reads nothing, but must still be at EOF: a file that grew
		// from zero after its Stat is refused like any other growth.
		return []byte{}, requireEOF(r)
	}
	buf := make([]byte, size)
	if _, err := io.ReadFull(r, buf); err != nil {
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			return nil, fmt.Errorf("store: object is shorter than its recorded size")
		}
		return nil, err
	}
	if err := requireEOF(r); err != nil {
		return nil, err
	}
	return buf, nil
}

// requireEOF probes one byte and refuses any that remain: a Read that returns data means the file has
// more bytes than its Stat reported. Only an explicit EOF is accepted; a reader
// making no progress has not established the end of the object.
func requireEOF(r io.Reader) error {
	var probe [1]byte
	switch n, err := r.Read(probe[:]); {
	case n > 0:
		return fmt.Errorf("store: object grew past its recorded size")
	case err == io.EOF:
		return nil
	case err != nil:
		return err
	default:
		return io.ErrNoProgress
	}
}

// ErrDamaged reports that an object was FOUND on disk and REFUSED: its physical size, its zstd
// frame, its indexed length or its content address did not hold up, so the bytes exist but may not
// be served.
//
// It wraps core.ErrNotFound because every caller written before it treated the two as one, and
// changing that silently would turn a degradation into a hard failure somewhere far from here.
// What it adds is the distinction §12.3 actually cares about, and finding S-3 is what happens
// without it: a quarantined object reached the model as `miss` — that is, as ABSENT, "the content
// was never there" — when what happened is that it was refused and preserved. A caller that wants
// the stronger fact tests errors.Is(err, store.ErrDamaged) BEFORE core.ErrNotFound; one that does
// not keeps the behaviour it had.
var ErrDamaged = fmt.Errorf("%w: object refused as damaged", core.ErrNotFound)

// Quarantiner is the §12.3 single-object quarantine, as a capability a caller can ask a Store for.
//
// It is deliberately not part of the Store interface: quarantining is not something a writer does
// in the ordinary course of storing anything, and widening Store would oblige every test double in
// the tree to grow a method none of them mean. A caller that has already decided an object is
// damaged — `qompack fsck --repair` is the one in this build — type-asserts for this instead of
// reaching for GetChunk and relying on its side effect to perform the move (R5-2).
type Quarantiner interface {
	Quarantine(h core.Hash, reason string) error
}

// Quarantine moves h's object file into tmp/quarantine/ as evidence, Louds once, and counts it —
// the same operation getObject performs when it rejects what it read, and now the only
// implementation of it.
//
// It reports:
//   - ErrReadOnly on a read-only store, which never moves anything, and core.ErrDegraded on a closed
//     one. `qompack fsck`'s fidelity pass runs through store.OpenReadOnly precisely so that ASKING
//     about a damaged root cannot relocate it; that property is enforced here rather than remembered
//     at each call site.
//   - core.ErrNotFound when no object file for h is on disk, so "nothing to move" is distinguishable
//     from "moved".
//
// A failed move is not an error to the caller: quarantine keeps the source intact and Louds, which
// is §12.3's "quarantine, Loud, continue" with the move part unavailable. The object still must not
// be served, and that is the caller's own refusal to make.
func (s *FSStore) Quarantine(h core.Hash, reason string) error {
	// mutate(), not a bare readOnly check: the refusal is a property of the METHOD, which is what
	// keeps a caller that reaches the *FSStore behind a narrow value from writing through it, and
	// what TestReadOnly_EveryExportedMethodIsClassified partitions the exported surface by.
	if err := s.mutate(); err != nil {
		return err
	}
	for _, p := range s.objectCandidates(h) {
		if _, err := os.Stat(paths.Long(p)); err != nil {
			continue
		}
		s.quarantine(h, p, reason)
		return nil
	}
	return fmt.Errorf("%w: object %s", core.ErrNotFound, h.Short())
}

// quarantine preserves rejected bytes in a unique attempt directory. A failed move leaves the
// source intact for recovery; the caller still refuses its contents. Each attempt has its own
// destination so concurrent or repeated corruption cannot overwrite earlier evidence.
func (s *FSStore) quarantine(h core.Hash, path, reason string) {
	if s.readOnly {
		// A read-only store reports the rejection and leaves the bytes exactly where they are
		// (readonly.go): the caller still refuses the contents, and `qompack fsck --repair` is what
		// moves the object once an operator has asked for it.
		s.log.Loud("store: object rejected; read-only store leaves it in place",
			"hash", h.Short(), "reason", reason)
		s.count("store.quarantine_skipped_read_only", 1)
		return
	}
	root := filepath.Join(s.l.Tmp, quarantineDir)
	err := os.MkdirAll(paths.Long(root), 0o700)
	if err == nil {
		var attempt string
		attempt, err = os.MkdirTemp(paths.Long(root), "object-")
		if err == nil {
			err = os.Rename(paths.Long(path), paths.Long(filepath.Join(attempt, filepath.Base(path))))
		}
	}
	if err != nil {
		s.log.Loud("store: object quarantine failed; source retained", "hash", h.Short(), "reason", reason)
		s.count("store.quarantine_failed", 1)
		return
	}
	s.log.Loud("store: object quarantined", "hash", h.Short(), "reason", reason)
	s.count("store.quarantined", 1)
}

// getObject returns h's plaintext bytes.
//
// wantLen is the length index/roots.jsonl recorded for this chunk, or a negative number when the
// caller has no recorded length to check against. Physical size, decompression, indexed length
// and the DomainChunk content address are checked before any plaintext is returned. A rejected
// object is quarantined when possible and reports ErrDamaged, which wraps core.ErrNotFound so every
// caller written against the legacy degradation keeps working while a caller that cares can tell
// "refused and preserved" from "absent" (finding S-3).
func (s *FSStore) getObject(h core.Hash, wantLen int) ([]byte, error) {
	raw, path, compressed, err := s.readObjectFile(h)
	if err != nil {
		return nil, err
	}

	plain := raw
	if compressed {
		decoded, decErr := Decode(raw)
		if decErr != nil {
			s.quarantine(h, path, "zstd decode failed")
			return nil, fmt.Errorf("%w: object %s failed to decode", ErrDamaged, h.Short())
		}
		plain = decoded
	}

	if wantLen >= 0 && len(plain) != wantLen {
		s.quarantine(h, path, fmt.Sprintf("length %d, index says %d", len(plain), wantLen))
		return nil, fmt.Errorf("%w: object %s has the wrong length", ErrDamaged, h.Short())
	}
	if core.HashBytes(core.DomainChunk, plain) != h {
		s.quarantine(h, path, "content hash mismatch")
		return nil, fmt.Errorf("%w: object %s has the wrong content hash", ErrDamaged, h.Short())
	}
	return plain, nil
}

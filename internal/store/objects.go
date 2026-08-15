package store

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
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
// It deliberately does NOT fsync each object. Durability for the store as a whole is batched into
// Flush — the SessionEnd/idle barrier that also syncs the append-only indices — exactly the way
// borg and restic commit a repository transaction rather than syncing every chunk.
//
// Per-object fsync was measured, not assumed, to be incompatible with this package's budgets: a
// 100 KB tool result is hundreds of chunks, and hundreds of serialized fsyncs cost ~1.9 s against
// a 3 ms budget (B-C, l0_process). The rename below still gives ATOMICITY, so a crash can never
// leave a torn or partially written object — only a missing one, which is a recoverable
// degradation the read path already handles: GetChunk reports core.ErrNotFound and Has falls back
// to a stat rather than trusting the index.
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
		b, readErr := os.ReadFile(paths.Long(p))
		if readErr == nil {
			return b, p, i == 0, nil
		}
		if !os.IsNotExist(readErr) {
			return nil, p, i == 0, fmt.Errorf("%w: reading object %s: %v", core.ErrNotFound, h.Short(), readErr)
		}
	}
	return nil, "", false, fmt.Errorf("%w: object %s", core.ErrNotFound, h.Short())
}

// quarantine moves a corrupt object out of objects/ and into .qompack/tmp/quarantine, so a
// later read cannot keep tripping over it and a human can still inspect it.
//
// This is the §12.3 "store corrupt" row verbatim: the object is removed from service, the failure
// is announced on the Loud channel, and the read reports core.ErrNotFound so the caller degrades
// to "I cannot re-materialize this" rather than crashing the session.
func (s *FSStore) quarantine(h core.Hash, path, reason string) {
	dst := filepath.Join(s.l.Tmp, quarantineDir, filepath.Base(path))
	if err := os.MkdirAll(paths.Long(filepath.Dir(dst)), 0o700); err == nil {
		if err := os.Rename(paths.Long(path), paths.Long(dst)); err != nil {
			// Losing the race to move it is not worth failing the read over; the object is
			// already known bad and the Loud line below still records that.
			_ = os.Remove(paths.Long(path))
		}
	}
	s.log.Loud("store: object quarantined", "hash", h.Short(), "reason", reason)
	s.count("store.quarantined", 1)
}

// getObject returns h's plaintext bytes.
//
// wantLen is the length index/roots.jsonl recorded for this chunk, or a negative number when the
// caller has no recorded length to check against. A decode failure — which zstd's per-frame
// content checksum makes reliable — or a length that disagrees with the index quarantines the
// object and reports core.ErrNotFound.
func (s *FSStore) getObject(h core.Hash, wantLen int) ([]byte, error) {
	raw, path, compressed, err := s.readObjectFile(h)
	if err != nil {
		return nil, err
	}

	plain := raw
	if compressed {
		decoded, decErr := Decode(raw)
		if decErr != nil {
			s.quarantine(h, path, "zstd decode failed: "+decErr.Error())
			return nil, fmt.Errorf("%w: object %s failed to decode", core.ErrNotFound, h.Short())
		}
		plain = decoded
	}

	if wantLen >= 0 && len(plain) != wantLen {
		s.quarantine(h, path, fmt.Sprintf("length %d, index says %d", len(plain), wantLen))
		return nil, fmt.Errorf("%w: object %s has the wrong length", core.ErrNotFound, h.Short())
	}
	return plain, nil
}

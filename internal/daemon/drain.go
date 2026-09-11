package daemon

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/obs"
	"github.com/qompack/qompack/internal/observer"
	"github.com/qompack/qompack/internal/paths"
)

// drainLineDeadline bounds how long Drain waits for a single dispatched request to return
// (task-3-spec.md drain.go: "a per-line context deadline of 5s").
const drainLineDeadline = 5 * time.Second

// drainReadBufferBytes sizes the buffered reader Drain scans each spool file with.
const drainReadBufferBytes = 64 << 10 // 64 KiB

// The two filename families Drain (via ipc.SpoolFiles) distinguishes. (The blob-descriptor field
// name and shape live in blob.go, shared with ingest.go's dispatch path.)
const (
	drainWalPrefix = "wal-"
	drainFileExt   = ".ndjson"
)

// drainStateFile is state/drain.json's filename.
const drainStateFile = "drain.json"

// drainFileState is one spool file's persisted progress.
type drainFileState struct {
	Size   int64 `json:"size"`
	Offset int64 `json:"offset"`
	Done   bool  `json:"done"`
	// PendingBlobs is persisted with the acknowledged offset before any deletion is attempted.
	PendingBlobs []string `json:"pending_blobs,omitempty"`
}

// drainState is state/drain.json's on-disk shape, keyed by spool file base name.
type drainState map[string]*drainFileState

// DrainConfig is drainer's dependency set.
type DrainConfig struct {
	Root    string
	Log     logging.Logger
	Metrics obs.Registry
	Clock   core.Clock
	// Dispatch is the same handler the worker pool uses to process a request.
	Dispatch func(ctx context.Context, req ipc.Request) ipc.Response
	// Seen shares in-flight ownership and successful handling within this daemon lifetime. It
	// is not a durable delivery identity: restart may redeliver, and equal-content identity is
	// a remaining SP-20 lease migration requirement.
	Seen *seenSet
	// Admit is the daemon-side privacy gate applied to every inherited spool record before it is
	// dispatched or persisted. A nil Admit admits everything — the pre-gate behaviour, kept so a
	// bare drainer fixture with no daemon behind it still works.
	Admit func(ipc.Request) admissionVerdict
	// Journal resolves the held delivery journal, so a drained line can take back the identity its
	// original delivery was assigned and can write the committed-frontier record that releases it.
	Journal func() (*deliveryJournal, error)
	// IsLive reports whether sess is still a live session; its WAL is kept (offset-marked, not
	// deleted) rather than removed once fully drained. A nil IsLive treats every session as ended,
	// so a bare drainer with no wired registry still deletes fully-drained files.
	IsLive func(sess core.SessionID) bool
	// RemoveWAL removes a fully drained wal-* segment on the drainer's behalf and reports whether it
	// did. The daemon wires the ingest's removeDrainedWAL, which refuses a segment the ingest holds
	// open for appending and one whose size no longer equals drained, deciding both under the mutex
	// every append takes. A nil RemoveWAL (a bare drainer with no ingest behind it) falls back to
	// removeIfUnchanged: the same size check, without that exclusion.
	RemoveWAL func(path string, drained int64) (removed bool, err error)
	// HoldsWAL reports whether the ingest holds the wal-* segment at path open for appending, the
	// first of RemoveWAL's two refusals. The drainer asks before it forgets a finished segment's
	// progress, and leaves a held one alone. Without the question, every pass over such a segment
	// (each idle tick, for a straggler after SessionEnd or an EndAbandoned session whose handle
	// stays cached) forgot its progress, had the removal refused and put the progress back: two
	// extra state/drain.json writes, and each time a window in which a crash leaves the file with
	// no entry. The answer is advisory. A segment that becomes held after it is still refused by
	// RemoveWAL, which decides under the lock every append takes, and its progress is put back. A
	// nil HoldsWAL skips the question.
	HoldsWAL func(path string) bool
}

// drainer is a standalone drain engine (task-3-spec.md drain.go's algorithm), independent of the
// Daemon interface: Task 4 wires it into Daemon.Drain by constructing one from the running
// daemon's own dependencies and dispatch function.
type drainer struct {
	cfg DrainConfig
	mu  sync.Mutex // serializes concurrent Drain calls (idle tick vs. admin.drain) against one drain.json

	gapMu sync.Mutex
	gaps  DrainGapState
}

// newDrainer returns a drainer over cfg, filling in nil-safe defaults.
func newDrainer(cfg DrainConfig) *drainer {
	if cfg.Log == nil {
		cfg.Log = logging.Nop()
	}
	if cfg.Clock == nil {
		cfg.Clock = core.SystemClock()
	}
	if cfg.Dispatch == nil {
		cfg.Dispatch = func(context.Context, ipc.Request) ipc.Response { return ipc.Response{} }
	}
	if cfg.IsLive == nil {
		cfg.IsLive = func(core.SessionID) bool { return false }
	}
	return &drainer{cfg: cfg}
}

// Drain replays every spool-tier file under root's spool directory (task-3-spec.md drain.go):
// wal-* first, then client-*, each group lexically sorted (ipc.SpoolFiles already orders them).
// It is idempotent (state/drain.json records consumed offsets) and resumable: a cancelled Drain
// persists its progress and returns (n, ctx.Err()), so the next call picks up exactly where it
// stopped. Per-file errors are logged, counted, and never abort the rest of the drain.
func (dr *drainer) Drain(ctx context.Context) (int, error) {
	dr.mu.Lock()
	defer dr.mu.Unlock()

	spoolDir := paths.Of(dr.cfg.Root).Spool
	files, err := ipc.SpoolFiles(spoolDir)
	if err != nil {
		return 0, err
	}

	gaps := &gapRecorder{}
	st, err := dr.loadState()
	if err != nil {
		gaps.add("", DrainGapProgressUnreadable, "drain progress state is unreadable")
		dr.publishGaps(gaps.state(dr.cfg.Clock, 0))
		return 0, err // preserve corrupt progress for diagnosis; never authorize deletion from it
	}
	if err := dr.validateProgress(files, st); err != nil {
		gaps.add("", DrainGapProgressUnreadable, "drain progress no longer matches the spool")
		dr.publishGaps(gaps.state(dr.cfg.Clock, 0))
		return 0, err
	}
	listed := make(map[string]bool, len(files))
	for _, path := range files {
		listed[filepath.Base(path)] = true
	}
	if err := dr.forgetReleased(st, listed); err != nil {
		return dr.unpersisted(gaps, err)
	}
	total := 0
	stopErr := dr.cleanupAcknowledged(st)
	// The cleanup can consume the last intent of an entry whose file is already gone; that entry
	// is released now, and forgotten before the first file is drained like the rest.
	if err := dr.forgetReleased(st, listed); err != nil {
		return dr.unpersisted(gaps, errors.Join(stopErr, err))
	}

	for _, path := range files {
		if ctx.Err() != nil {
			stopErr = errors.Join(stopErr, ctx.Err())
			break
		}
		n, ferr := dr.drainFile(ctx, path, st, gaps)
		total += n
		if ferr == nil {
			continue
		}
		if errors.Is(ferr, context.Canceled) || errors.Is(ferr, context.DeadlineExceeded) {
			stopErr = errors.Join(stopErr, ferr)
			break
		}
		if dr.cfg.Metrics != nil {
			dr.cfg.Metrics.Counter(counterDrainFileError).Add(1)
		}
		dr.cfg.Log.Warn("daemon: drain: file error", "path", path, "err", ferr)
		stopErr = errors.Join(stopErr, ferr)
	}

	if serr := dr.saveState(st); serr != nil {
		dr.cfg.Log.Warn("daemon: drain: failed to persist state", "err", serr)
		stopErr = errors.Join(stopErr, serr)
	}
	pending := int64(0)
	for base, fs := range st {
		if remaining := fs.Size - fs.Offset; remaining > 0 {
			pending += remaining
			gaps.add(base, DrainGapPending, "spool bytes not yet replayed")
		}
	}
	dr.publishGaps(gaps.state(dr.cfg.Clock, pending))
	return total, stopErr
}

// drainFile drains one spool file, updating st in place. It returns the number of requests
// dispatched and, if ctx was cancelled mid-file, ctx.Err() — otherwise nil, even when individual
// corrupt lines were skipped (those are reported via the metrics/log side channel, not the
// returned error, so one bad line never aborts the rest of the file).
func (dr *drainer) drainFile(ctx context.Context, path string, st drainState, gaps *gapRecorder) (int, error) {
	base := filepath.Base(path)

	fi, err := os.Stat(paths.Long(path))
	if err != nil {
		if os.IsNotExist(err) {
			// Listed, then gone before this stat: something other than the drainer deleted it. Its
			// entry goes with it unless it still carries cleanup intents, the only record of blobs
			// cleanupAcknowledged must still remove; forgetReleased keeps such an entry too.
			if gone := st[base]; gone == nil || len(gone.PendingBlobs) == 0 {
				delete(st, base)
			}
			return 0, nil
		}
		return 0, err
	}
	size := fi.Size()

	fs, ok := st[base]
	if !ok {
		fs = &drainFileState{}
		st[base] = fs
	}
	if fs.Done && fs.Size == size {
		return 0, dr.removeCompletedFile(path, base, fs, st)
	}
	fs.Done = false

	f, err := os.Open(paths.Long(path))
	if err != nil {
		return 0, err
	}
	defer func() { _ = f.Close() }() // also releases the handle if a callback panics or exits its goroutine
	// Also close explicitly before a possible os.Remove: Windows cannot delete an open file.

	if fs.Offset > 0 {
		if _, err := f.Seek(fs.Offset, io.SeekStart); err != nil {
			_ = f.Close()
			return 0, err
		}
	}

	// The pass reads the file as it stood at the stat above and no further. Bytes appended after it
	// — by the ingest to a WAL segment it holds, by a hook process to its own client spool — belong
	// to the next pass. Reading them too let the consumed offset overtake the size recorded below,
	// and loadState refuses exactly that as inconsistent progress, so every later Drain failed
	// before reading a single spool file.
	r := bufio.NewReaderSize(io.LimitReader(f, size-fs.Offset), drainReadBufferBytes)
	offset := fs.Offset
	count := 0
	canceled := false
	var readErr error

readLoop:
	for {
		select {
		case <-ctx.Done():
			canceled = true
			break readLoop
		default:
		}

		raw, err := r.ReadBytes('\n')
		if err != nil {
			if errors.Is(err, io.EOF) {
				break readLoop // a trailing partial line, if any, is left unconsumed
			}
			readErr = err
			break readLoop
		}
		nextOffset := offset + int64(len(raw))

		line := bytes.TrimSuffix(raw, []byte{'\n'})
		if len(bytes.TrimSpace(line)) == 0 {
			offset, fs.Offset = nextOffset, nextOffset
			continue // lenient to blank lines, though the writer never emits them
		}

		req, decErr := ipc.DecodeRequest(line)
		if decErr != nil {
			if dr.cfg.Metrics != nil {
				dr.cfg.Metrics.Counter(counterDrainFileError).Add(1)
			}
			dr.cfg.Log.Warn("daemon: drain: corrupt line", "path", path, "err", decErr)
			gaps.add(base, DrainGapCorruptLine, "line did not decode")
			offset, fs.Offset = nextOffset, nextOffset
			continue
		}

		// Privacy admission for an INHERITED record. A record that already carries a decision keeps
		// it — re-deciding could only work from the derived Event, which cannot restore what the
		// first policy removed. A record that carries none is decided here, before it is dispatched
		// and therefore before anything it would cause can be persisted.
		verdict := dr.admitLine(req)
		switch {
		case verdict.Denied:
			// A denial is terminal: retrying produces the same answer, so the offset advances and
			// the record is released. Nothing was persisted and nothing will be.
			gaps.add(base, DrainGapDenied, verdict.Reason)
			offset, fs.Offset = nextOffset, nextOffset
			continue
		case verdict.Failed:
			// A failure is terminal FOR THIS RECORD, and the offset advances past it.
			//
			// This branch used to leave the line where it was, on the reasoning that a later pass
			// — with a readable policy, or a repaired configuration — could still admit it. That
			// reasoning holds for a policy that failed to compile, but the decision is BAKED INTO
			// the spooled bytes: no later pass can change this record's Capture, so no later pass
			// can ever admit it. Leaving it in place wedged the file permanently — the same
			// offset, the same verdict, the same abandonment on every startup and every idle tick
			// — and every record BEHIND it was never delivered for the life of the project, while
			// the records in front of it were re-dispatched on every pass.
			//
			// Losing one unadmittable record LOUDLY is correct; losing everything behind it
			// silently is not. The gap is recorded, counted and announced, and the drain goes on.
			if dr.cfg.Metrics != nil {
				dr.cfg.Metrics.Counter(counterDrainUnadmitted).Add(1)
			}
			dr.cfg.Log.Loud("daemon: drain: capture not admitted; record skipped",
				"path", path, "reason", verdict.Reason)
			gaps.add(base, DrainGapUnadmitted, verdict.Reason)
			offset, fs.Offset = nextOffset, nextOffset
			continue
		case verdict.Degraded:
			// The policy decided and the decision is degraded. It is admitted exactly as the live
			// path admits it: dispatchPending publishes the sidecar, and runIngested withholds
			// only the Event it never had. Falling through is the whole point.
		}
		req = verdict.Request

		lease, leased := dr.leaseDelivery(ctx, req)
		if !leased {
			gaps.add(base, DrainGapUnleased, "delivery has no durable identity")
		}
		// The committed frontier is consulted BEFORE the seen set, for every leased line. One
		// delivery can reach the drain as two copies: the daemon's own WAL copy, and the hook
		// client's fallback copy (ipc spoolAndReturn, taken when the one-byte transport ACK misses
		// its deadline AFTER the daemon had already leased, published and acknowledged the
		// request). The seen set cannot witness either across a restart — it is process memory,
		// empty on a fresh daemon — but the frontier is durable, and delivery_lease.go's
		// acknowledge states the contract for a copy it already names: "a redelivery already
		// acknowledged returns without appending, which is what lets a drained line advance a
		// spool offset without republishing anything". Dispatching such a copy re-ran the observer
		// through fresh handlers for a delivery the frontier held (F4-P1). The copy takes exactly
		// the path the completed branch below takes for its acknowledged case: its blob stays
		// pending until this file's consumed offset is persisted, the offset advances, and nothing
		// is dispatched, gapped or recorded in the seen set. A leased copy the frontier does NOT
		// name — the crash window between publication and acknowledgement — falls through and is
		// re-dispatched under its reused lease, as before.
		if leased && dr.acknowledgedDelivery(lease, leased) {
			if _, blob, blobErr := readBlob(dr.cfg.Root, req); blobErr == nil && blob != "" {
				fs.PendingBlobs = append(fs.PendingBlobs, blob)
			}
			offset, fs.Offset = nextOffset, nextOffset
			continue
		}
		key := deliveryIdentityKey(lease, leased, line)
		if dr.cfg.Seen != nil {
			completed, acquired := dr.cfg.Seen.begin(key)
			if completed {
				if leased && !dr.acknowledgedDelivery(lease, leased) {
					gaps.add(base, DrainGapUnacknowledged, "in-memory completion has no frontier record")
				}
				// A live worker's acknowledgement was in memory only. Keep its blob until this
				// file's consumed offset is persisted below.
				if _, blob, blobErr := readBlob(dr.cfg.Root, req); blobErr == nil && blob != "" {
					fs.PendingBlobs = append(fs.PendingBlobs, blob)
				}
				offset, fs.Offset = nextOffset, nextOffset
				continue
			}
			if !acquired {
				readErr = fmt.Errorf("daemon: drain: delivery still in progress")
				break readLoop
			}
		}

		blob, dispatchErr := dr.dispatchPending(ctx, req, lease, leased)
		if dr.cfg.Seen != nil {
			dr.cfg.Seen.finish(key, dispatchErr == nil)
		}
		if dispatchErr != nil {
			gaps.add(base, DrainGapUnacknowledged, "publication did not reach the frontier")
			readErr = dispatchErr
			break readLoop
		}
		if blob != "" {
			fs.PendingBlobs = append(fs.PendingBlobs, blob)
		}
		offset, fs.Offset = nextOffset, nextOffset
		count++
	}
	_ = f.Close() // must happen before the delete-if-drained check below (Windows cannot remove an open file)

	fs.Size = size
	if canceled {
		return count, ctx.Err()
	}
	if readErr != nil {
		// Persist the progress this pass DID make before surfacing the error. fs.Offset advances
		// only past a record that was fully dispatched and acknowledged (or explicitly accounted
		// for as a gap), so saving here can never release an undelivered record — while NOT
		// saving re-dispatches every line ahead of the failure on the next pass, forever.
		if serr := dr.saveState(st); serr != nil {
			dr.cfg.Log.Warn("daemon: drain: failed to persist progress after a read error", "err", serr)
		}
		return count, readErr
	}

	fs.Done = offset == size // incomplete trailing bytes remain pending, even for ended sessions
	// Persisted here — per file, on EOF, before the cleanup and the remove — not just once at the
	// end of Drain (task-3-spec.md drain.go step 4's exact ordering): a crash anywhere after this
	// file's last line must not lose its recorded completion or its cleanup intents, which is what
	// makes "Drain is idempotent and resumable" true across a crash, not only across a clean
	// cancel. The completion is given up only for a removal, and removeCompletedFile persists that
	// before it unlinks.
	if serr := dr.saveState(st); serr != nil {
		dr.cfg.Log.Warn("daemon: drain: failed to persist state", "err", serr)
		return count, serr
	}
	if err := dr.cleanupAcknowledged(st); err != nil {
		return count, err
	}
	return count, dr.removeCompletedFile(path, base, fs, st)
}

// removeCompletedFile removes a fully drained file whose cleanup intents are all consumed, if
// shouldDelete allows it — and only if the file still ends where the drain stopped. fs.Offset is
// the consumed offset just persisted; the file's size at removal time is re-read rather than taken
// from the stat at the top of the pass, because a file can grow between that stat and here. A file
// that has grown, or a WAL segment the ingest still holds, is left for a later pass. Neither is an
// error: nothing is wrong with it, it simply is not finished yet.
//
// The file's progress entry is forgotten, durably, BEFORE the unlink. Forgetting it afterwards, in
// memory for the next saveState, let a crash in between keep {Done, Offset: S, Size: S} on disk for
// a file that was gone, and nothing retired it: loadState accepts it, the pass visits only files
// that exist, and every later save wrote it back. Once the name came back (the ingest reopening
// wal-<session>.ndjson after a restart mid-session, a straggler after SessionEnd reopening segment
// 0), a smaller file failed every Drain for the whole spool, one of the same size was removed
// undrained, and a larger one lost its first S bytes. Forgetting first moves the crash window to
// the safe side: a file with no entry, which the next pass reads again from offset zero. That is
// at-least-once, which the drain already collapses for every leased line through the committed
// frontier, and which the handlers tolerate for the rest. A removal that does not happen — refused
// or failed — puts the entry back and persists it again before the pass goes on.
//
// A WAL segment the ingest holds is left before any of that (DrainConfig.HoldsWAL). RemoveWAL would
// refuse it, and forgetting it only to put it back cost two state/drain.json writes on every pass
// over it and reopened, each time, the window in which a crash leaves the file with no entry.
func (dr *drainer) removeCompletedFile(path, base string, fs *drainFileState, st drainState) error {
	if !fs.Done || len(fs.PendingBlobs) > 0 || !dr.shouldDelete(base) {
		return nil
	}
	_, isWAL := walSessionID(base)
	if isWAL && dr.cfg.HoldsWAL != nil && dr.cfg.HoldsWAL(path) {
		return nil // RemoveWAL would refuse it: nothing to forget, nothing to put back
	}
	remove := removeIfUnchanged
	if isWAL && dr.cfg.RemoveWAL != nil {
		remove = dr.cfg.RemoveWAL
	}
	delete(st, base)
	if err := dr.saveState(st); err != nil {
		st[base] = fs // nothing is removed until its forgetting is on disk
		return err
	}
	removed, err := remove(path, fs.Offset)
	if removed || errors.Is(err, os.ErrNotExist) {
		return nil // gone, and already forgotten on disk
	}
	st[base] = fs
	if serr := dr.saveState(st); serr != nil {
		return errors.Join(err, serr)
	}
	return err
}

// removeIfUnchanged removes path only if its size still equals drained, so bytes appended after a
// pass read to EOF are not deleted along with the file. With no writer-side exclusion it narrows the
// window rather than closing it: an append landing between the stat and the unlink is still lost on
// POSIX, while on Windows a writer that still holds the file makes the remove fail and a later pass
// retries. For a client-<pid>.ndjson file that residual is a hook process appending in that instant,
// or appending again after its earlier lines were drained (and, on POSIX, into a file already
// unlinked under it); the ingest's WAL segments do not rely on it (ingest.removeDrainedWAL).
func removeIfUnchanged(path string, drained int64) (bool, error) {
	fi, err := os.Stat(paths.Long(path))
	if err != nil {
		return false, err
	}
	if fi.Size() != drained {
		return false, nil
	}
	if err := os.Remove(paths.Long(path)); err != nil {
		return false, err
	}
	return true, nil
}

// cleanupAcknowledged consumes only cleanup intents from a successfully persisted state. Every
// failed deletion remains in that state for the next drain/restart, even after the source ended.
func (dr *drainer) cleanupAcknowledged(st drainState) error {
	pending := false
	for _, fs := range st {
		pending = pending || len(fs.PendingBlobs) > 0
	}
	if !pending {
		return nil
	}
	// A lost transport ACK can leave the same descriptor in both WAL and client fallback.
	// A cleanup intent proves one consumed reference, not that every other reference is gone.
	referenced, err := dr.pendingBlobReferences(st)
	if err != nil {
		return err // incomplete/unreadable input cannot authorize collection
	}
	var result error
	for _, fs := range st {
		var remaining []string
		for _, blob := range fs.PendingBlobs {
			if referenced[blob] {
				remaining = append(remaining, blob)
				continue
			}
			if err := removeBlob(dr.cfg.Root, blob); err != nil {
				remaining = append(remaining, blob)
				result = errors.Join(result, err)
			}
		}
		fs.PendingBlobs = remaining
	}
	return result
}

// pendingBlobReferences checks the current spool snapshot only. A future delivery/lease ledger
// must also protect descriptors that a live client can publish after this scan.
func (dr *drainer) pendingBlobReferences(st drainState) (map[string]bool, error) {
	files, err := ipc.SpoolFiles(paths.Of(dr.cfg.Root).Spool)
	if err != nil {
		return nil, err
	}
	refs := make(map[string]bool)
	for _, path := range files {
		if err := scanPendingBlobs(path, st[filepath.Base(path)], refs); err != nil {
			return nil, err
		}
	}
	return refs, nil
}

func scanPendingBlobs(path string, fs *drainFileState, refs map[string]bool) error {
	f, err := os.Open(paths.Long(path))
	if err != nil {
		return fmt.Errorf("daemon: drain: blob reference source unavailable")
	}
	defer func() { _ = f.Close() }()
	if fs != nil {
		if _, err := f.Seek(fs.Offset, io.SeekStart); err != nil {
			return err
		}
	}
	s := bufio.NewScanner(f)
	s.Buffer(make([]byte, drainReadBufferBytes), ipc.MaxLineBytes+1)
	s.Split(func(data []byte, atEOF bool) (int, []byte, error) {
		if atEOF && len(data) > 0 && !bytes.ContainsRune(data, '\n') {
			return 0, nil, fmt.Errorf("daemon: drain: incomplete blob reference source")
		}
		return bufio.ScanLines(data, atEOF)
	})
	for s.Scan() {
		if len(bytes.TrimSpace(s.Bytes())) == 0 {
			continue
		}
		req, err := ipc.DecodeRequest(s.Bytes())
		if err != nil {
			return fmt.Errorf("daemon: drain: invalid blob reference source")
		}
		var ref blobRef
		if json.Unmarshal(req.Raw, &ref) == nil && ref.Blob != "" && ref.Field == drainBlobToolResponse {
			refs[ref.Blob] = true
		}
	}
	return s.Err()
}

func (dr *drainer) validateProgress(files []string, st drainState) error {
	for _, path := range files {
		fs := st[filepath.Base(path)]
		if fs == nil {
			continue
		}
		fi, err := os.Stat(paths.Long(path))
		if err != nil || fs.Size > fi.Size() || fs.Offset > fi.Size() {
			return fmt.Errorf("daemon: drain: progress no longer matches spool")
		}
	}
	return nil
}

// forgetReleased drops the progress entry of every released file — finished (Done), no cleanup
// intent left — whose base name is not in listed, the pass's spool listing, and persists st if it
// dropped one. Such an entry describes a file that is gone. removeCompletedFile forgets a file
// before it unlinks it, but state already on disk may predate that ordering: its crash window left
// an entry whose file was unlinked and whose save never happened, and the entry was then applied to
// whatever file later took the same name. A name missing from the listing proves the entry's file
// is gone even if the name comes back a moment later, which a stat taken now could not tell apart.
// An entry that still carries cleanup intents stays: they are the only record of blobs
// cleanupAcknowledged must still remove. So does an unfinished one, whose file the drain never
// removes.
func (dr *drainer) forgetReleased(st drainState, listed map[string]bool) error {
	forgot := false
	for base, fs := range st {
		if fs.Done && len(fs.PendingBlobs) == 0 && !listed[base] {
			delete(st, base)
			forgot = true
		}
	}
	if !forgot {
		return nil
	}
	return dr.saveState(st)
}

// unpersisted ends a pass that could not persist its own progress before draining a single file.
// Like progress it cannot read, that is a gap in the whole replay.
func (dr *drainer) unpersisted(gaps *gapRecorder, err error) (int, error) {
	gaps.add("", DrainGapProgressUnreadable, "drain progress could not be persisted")
	dr.publishGaps(gaps.state(dr.cfg.Clock, 0))
	return 0, err
}

// dispatchPending leaves the record and any externalized bytes available until handling and
// acknowledgement persistence succeed. A NAK, panic, or canceled handler cannot consume it.
// It enforces publication order for a drained record exactly as the ingest worker does for a live
// one: durable capture, then the reference the dispatch writes, then the committed frontier. The
// offset in drainFile advances only when this returns nil, so a delivery that did not reach the
// frontier is redelivered rather than silently released.
func (dr *drainer) dispatchPending(ctx context.Context, req ipc.Request, lease deliveryLease, leased bool) (blob string, err error) {
	defer func() {
		if recover() != nil {
			err = fmt.Errorf("daemon: drain: handler panicked")
		}
	}()
	resolved, blob, err := readBlob(dr.cfg.Root, req)
	if err != nil {
		return "", err
	}
	if leased {
		if err := publishCapture(dr.cfg.Root, resolved, lease); err != nil {
			return "", fmt.Errorf("daemon: drain: capture not durable: %w", err)
		}
	}
	dctx, cancel := context.WithTimeout(ctx, drainLineDeadline)
	defer cancel()
	resp := dr.cfg.Dispatch(observer.WithObservation(dctx, lease.ObservationID), resolved)
	if !resp.OK || resp.Err != "" {
		if dctx.Err() != nil {
			return "", dctx.Err()
		}
		return "", fmt.Errorf("daemon: drain: handler did not acknowledge delivery")
	}
	if err := dr.commitDelivery(ctx, lease, leased); err != nil {
		return "", err
	}
	return blob, nil
}

// shouldDelete reports whether a fully-drained file should be removed: a client-*.ndjson fallback
// file always may be; a wal-<session>*.ndjson file may be only once IsLive reports the session is
// no longer live.
func (dr *drainer) shouldDelete(base string) bool {
	if sess, ok := walSessionID(base); ok {
		return !dr.cfg.IsLive(sess)
	}
	return true
}

// walSessionID extracts the session id from a wal-<session>.ndjson or
// wal-<session>.<rotationSeq>.ndjson base name. ok is false for anything else (a client-*.ndjson
// file, or an unrecognized name).
func walSessionID(base string) (core.SessionID, bool) {
	if !strings.HasPrefix(base, drainWalPrefix) || !strings.HasSuffix(base, drainFileExt) {
		return "", false
	}
	mid := strings.TrimSuffix(strings.TrimPrefix(base, drainWalPrefix), drainFileExt)
	if idx := strings.LastIndex(mid, "."); idx >= 0 {
		if _, err := strconv.Atoi(mid[idx+1:]); err == nil {
			mid = mid[:idx]
		}
	}
	if mid == "" {
		return "", false
	}
	return core.SessionID(mid), true
}

// drainStatePath returns <root>/.qompack/state/drain.json.
func drainStatePath(root string) string {
	return filepath.Join(paths.Of(root).State, drainStateFile)
}

// loadState distinguishes a first drain from unreadable or inconsistent progress. Corrupt state
// requires an explicit recovery decision; treating it as success could delete unread records.
func (dr *drainer) loadState() (drainState, error) {
	b, err := os.ReadFile(paths.Long(drainStatePath(dr.cfg.Root)))
	if os.IsNotExist(err) {
		return drainState{}, nil
	}
	if err != nil {
		return nil, err
	}
	var st drainState
	if err := json.Unmarshal(b, &st); err != nil || st == nil {
		return nil, fmt.Errorf("daemon: drain: invalid progress state")
	}
	for _, fs := range st {
		if fs == nil || fs.Size < 0 || fs.Offset < 0 || fs.Offset > fs.Size || (fs.Done && fs.Offset != fs.Size) {
			return nil, fmt.Errorf("daemon: drain: inconsistent progress state")
		}
		for _, blob := range fs.PendingBlobs {
			if !safeBlobName(blob) {
				return nil, fmt.Errorf("daemon: drain: invalid cleanup intent")
			}
		}
	}
	return st, nil
}

// saveState persists st to state/drain.json via paths.WriteAtomic. WriteAtomic renames its temp
// file onto the destination, which requires the destination's parent directory to already exist —
// a fresh project that has never had .qompack/state/ created (paths.EnsureLayout not yet run)
// would otherwise fail the rename silently on every call.
func (dr *drainer) saveState(st drainState) error {
	b, err := json.Marshal(st)
	if err != nil {
		return err
	}
	p := drainStatePath(dr.cfg.Root)
	if err := os.MkdirAll(paths.Long(filepath.Dir(p)), 0o700); err != nil {
		return err
	}
	return paths.WriteAtomic(p, b, 0o600)
}

// ---------------------------------------------------------------------------
// Drain gap state (M2-02's "index/drain gaps", produced here)
//
// internal/negknow can answer "absent" only for a question whose evidence it actually has. It
// deliberately does not model index/drain gaps, because the gap lives here: a spool file the drain
// has not finished, a line it could not admit, a delivery it could not lease or acknowledge. This
// is the producer side of that fact, exposed so a caller can tell "nothing was recorded" from "we
// cannot currently tell".

// counterDrainUnadmitted counts spool records the drain skipped because privacy admission could
// not decide them. It is declared here, beside the gap vocabulary it accompanies, because the two
// are read together: the counter says how often, the gap says which file and why.
const counterDrainUnadmitted = "drain_unadmitted"

// DrainGapKind is the closed set of reasons a drain cannot account for part of the spool.
type DrainGapKind string

const (
	// DrainGapCorruptLine is a spool line that would not decode. It was skipped; its content is
	// unrecoverable and its delivery is unaccounted for.
	DrainGapCorruptLine DrainGapKind = "corrupt_line"
	// DrainGapUnadmitted is a line privacy admission could not decide. Nothing was persisted and
	// nothing will be: the record carries its own capture, so no later pass sees anything
	// different. The offset advances past it so the records behind it are still delivered.
	DrainGapUnadmitted DrainGapKind = "unadmitted"
	// DrainGapDenied is a line privacy policy refused. Nothing was persisted and nothing will be:
	// this is a decision, not an outage, and it is reported so it is never read as coverage.
	DrainGapDenied DrainGapKind = "denied"
	// DrainGapUnleased is a delivery that reached the drain with no durable identity — no nonce,
	// or no journal to lease from.
	DrainGapUnleased DrainGapKind = "unleased"
	// DrainGapUnacknowledged is a delivery whose publication did not reach the committed frontier.
	// Its spool offset was not advanced, so it will be redelivered.
	DrainGapUnacknowledged DrainGapKind = "unacknowledged"
	// DrainGapPending is a spool file with bytes still unread when the pass ended.
	DrainGapPending DrainGapKind = "pending"
	// DrainGapProgressUnreadable is drain state this process refused to act on at all. It is the
	// strongest form of "cannot currently tell": no file was consulted.
	DrainGapProgressUnreadable DrainGapKind = "progress_unreadable"
)

// DrainGap is one accounted-for hole in the replay.
type DrainGap struct {
	File   string       `json:"file,omitempty"`
	Kind   DrainGapKind `json:"kind"`
	Count  int          `json:"count"`
	Reason string       `json:"reason,omitempty"`
}

// DrainGapState is the drain's own answer to "is the record complete?".
//
// Observed distinguishes the two cases callers must never merge: false means no drain has run in
// this process, so the answer is UNKNOWN and no absence claim may rest on it; true with no gaps and
// Complete set means the spool was fully replayed and acknowledged.
type DrainGapState struct {
	Observed     bool           `json:"observed"`
	Complete     bool           `json:"complete"`
	PendingBytes int64          `json:"pending_bytes"`
	Gaps         []DrainGap     `json:"gaps,omitempty"`
	UpdatedAt    core.UnixMilli `json:"updated_at,omitempty"`
}

// GapReporter is the optional seam a caller uses to read DrainGapState off a running Daemon. It is
// deliberately NOT a method on the Daemon interface: every existing implementation of that
// interface would otherwise have to grow one, and this is a diagnostic, not part of the contract a
// daemon must satisfy to run. Use the same guarded assertion observer_ops.go uses for Persister.
type GapReporter interface {
	// DrainGaps reports what the most recent replay could and could not account for.
	DrainGaps() DrainGapState
}

// gapRecorder accumulates one pass's gaps.
type gapRecorder struct {
	gaps map[DrainGap]int
}

func (g *gapRecorder) add(file string, kind DrainGapKind, reason string) {
	if g.gaps == nil {
		g.gaps = map[DrainGap]int{}
	}
	g.gaps[DrainGap{File: file, Kind: kind, Reason: reason}]++
}

func (g *gapRecorder) state(clk core.Clock, pending int64) DrainGapState {
	st := DrainGapState{Observed: true, PendingBytes: pending, UpdatedAt: core.NowMilli(clk)}
	for k, n := range g.gaps {
		k.Count = n
		st.Gaps = append(st.Gaps, k)
	}
	sort.Slice(st.Gaps, func(a, b int) bool {
		if st.Gaps[a].File != st.Gaps[b].File {
			return st.Gaps[a].File < st.Gaps[b].File
		}
		if st.Gaps[a].Kind != st.Gaps[b].Kind {
			return st.Gaps[a].Kind < st.Gaps[b].Kind
		}
		return st.Gaps[a].Reason < st.Gaps[b].Reason
	})
	st.Complete = len(st.Gaps) == 0 && pending == 0
	return st
}

// GapState returns the most recent pass's accounting. A drainer that has never run answers
// Observed:false, which is the honest "cannot currently tell" — not an empty set of gaps.
func (dr *drainer) GapState() DrainGapState {
	dr.gapMu.Lock()
	defer dr.gapMu.Unlock()
	st := dr.gaps
	st.Gaps = append([]DrainGap(nil), dr.gaps.Gaps...)
	return st
}

func (dr *drainer) publishGaps(st DrainGapState) {
	dr.gapMu.Lock()
	defer dr.gapMu.Unlock()
	dr.gaps = st
}

// admitLine applies the daemon-side privacy gate to an inherited spool record. A drainer with no
// Admit function (a bare test drainer) admits everything, which is the behaviour that existed
// before this gate and keeps a fixture that never had a capture working unchanged.
func (dr *drainer) admitLine(req ipc.Request) admissionVerdict {
	if dr.cfg.Admit == nil {
		return admissionVerdict{Request: req}
	}
	return dr.cfg.Admit(req)
}

// leaseDelivery mirrors ingest.leaseDelivery: the SAME nonce takes back the SAME lease, which is
// what makes a redelivered record reuse its original identity instead of acquiring a second one.
func (dr *drainer) leaseDelivery(ctx context.Context, req ipc.Request) (deliveryLease, bool) {
	if dr.cfg.Journal == nil || req.Nonce == "" {
		return deliveryLease{}, false
	}
	j, err := dr.cfg.Journal()
	if err != nil || j == nil {
		return deliveryLease{}, false
	}
	lease, err := j.lease(ctx, req.Nonce, req.Session, deliveryRequestHash(req))
	if err != nil {
		dr.cfg.Log.Warn("daemon: drain: delivery lease unavailable", "err", err)
		return deliveryLease{}, false
	}
	return lease, true
}

// commitDelivery writes the committed-frontier record for a drained delivery.
func (dr *drainer) commitDelivery(ctx context.Context, lease deliveryLease, leased bool) error {
	if !leased || dr.cfg.Journal == nil {
		return nil
	}
	j, err := dr.cfg.Journal()
	if err != nil || j == nil {
		return fmt.Errorf("daemon: drain: delivery journal unavailable for acknowledgement")
	}
	return j.acknowledge(ctx, lease.Delivery, lease.ObservationID, core.Hash{})
}

// acknowledgedDelivery reports whether the committed frontier already names this delivery.
func (dr *drainer) acknowledgedDelivery(lease deliveryLease, leased bool) bool {
	if !leased || dr.cfg.Journal == nil {
		return false
	}
	j, err := dr.cfg.Journal()
	if err != nil || j == nil {
		return false
	}
	return j.acknowledged(lease.Delivery)
}

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
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/obs"
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
	// IsLive reports whether sess is still a live session; its WAL is kept (offset-marked, not
	// deleted) rather than removed once fully drained. A nil IsLive treats every session as ended,
	// so a bare drainer with no wired registry still deletes fully-drained files.
	IsLive func(sess core.SessionID) bool
}

// drainer is a standalone drain engine (task-3-spec.md drain.go's algorithm), independent of the
// Daemon interface: Task 4 wires it into Daemon.Drain by constructing one from the running
// daemon's own dependencies and dispatch function.
type drainer struct {
	cfg DrainConfig
	mu  sync.Mutex // serializes concurrent Drain calls (idle tick vs. admin.drain) against one drain.json
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

	st, err := dr.loadState()
	if err != nil {
		return 0, err // preserve corrupt progress for diagnosis; never authorize deletion from it
	}
	if err := dr.validateProgress(files, st); err != nil {
		return 0, err
	}
	total := 0
	stopErr := dr.cleanupAcknowledged(st)

	for _, path := range files {
		if ctx.Err() != nil {
			stopErr = errors.Join(stopErr, ctx.Err())
			break
		}
		n, ferr := dr.drainFile(ctx, path, st)
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
	return total, stopErr
}

// drainFile drains one spool file, updating st in place. It returns the number of requests
// dispatched and, if ctx was cancelled mid-file, ctx.Err() — otherwise nil, even when individual
// corrupt lines were skipped (those are reported via the metrics/log side channel, not the
// returned error, so one bad line never aborts the rest of the file).
func (dr *drainer) drainFile(ctx context.Context, path string, st drainState) (int, error) {
	base := filepath.Base(path)

	fi, err := os.Stat(paths.Long(path))
	if err != nil {
		if os.IsNotExist(err) {
			delete(st, base)
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

	r := bufio.NewReaderSize(f, drainReadBufferBytes)
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
			offset, fs.Offset = nextOffset, nextOffset
			continue
		}

		key := core.HashBytes(walHashDomain, line)
		if dr.cfg.Seen != nil {
			completed, acquired := dr.cfg.Seen.begin(key)
			if completed {
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

		blob, dispatchErr := dr.dispatchPending(ctx, req)
		if dr.cfg.Seen != nil {
			dr.cfg.Seen.finish(key, dispatchErr == nil)
		}
		if dispatchErr != nil {
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
		return count, readErr
	}

	fs.Done = offset == size // incomplete trailing bytes remain pending, even for ended sessions
	// Persisted here — per file, on EOF, before the remove — not just once at the end of Drain
	// (task-3-spec.md drain.go step 4's exact ordering): a crash between this file's removal and
	// the end of the outer loop must not lose this file's recorded completion, which is what makes
	// "Drain is idempotent and resumable" true across a crash, not only across a clean cancel.
	if serr := dr.saveState(st); serr != nil {
		dr.cfg.Log.Warn("daemon: drain: failed to persist state", "err", serr)
		return count, serr
	}
	if err := dr.cleanupAcknowledged(st); err != nil {
		return count, err
	}
	return count, dr.removeCompletedFile(path, base, fs, st)
}

func (dr *drainer) removeCompletedFile(path, base string, fs *drainFileState, st drainState) error {
	if fs.Done && len(fs.PendingBlobs) == 0 && dr.shouldDelete(base) {
		if err := os.Remove(paths.Long(path)); err != nil {
			if !os.IsNotExist(err) {
				return err
			}
		} else {
			delete(st, base)
		}
	}
	return nil
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

// dispatchPending leaves the record and any externalized bytes available until handling and
// acknowledgement persistence succeed. A NAK, panic, or canceled handler cannot consume it.
func (dr *drainer) dispatchPending(ctx context.Context, req ipc.Request) (blob string, err error) {
	defer func() {
		if recover() != nil {
			err = fmt.Errorf("daemon: drain: handler panicked")
		}
	}()
	resolved, blob, err := readBlob(dr.cfg.Root, req)
	if err != nil {
		return "", err
	}
	dctx, cancel := context.WithTimeout(ctx, drainLineDeadline)
	defer cancel()
	resp := dr.cfg.Dispatch(dctx, resolved)
	if !resp.OK || resp.Err != "" {
		if dctx.Err() != nil {
			return "", dctx.Err()
		}
		return "", fmt.Errorf("daemon: drain: handler did not acknowledge delivery")
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

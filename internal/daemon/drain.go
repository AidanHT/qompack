package daemon

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
	// Seen, if non-nil, is consulted (and updated) before every dispatch, so a line already
	// processed by the ingest worker pool this daemon lifetime — or by an earlier Drain call — is
	// never dispatched twice. Sharing the same *seenSet the ingest queue uses is what makes a
	// NAK-then-spooled duplicate line collapse to exactly one dispatch.
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

	st := dr.loadState()
	total := 0
	var stopErr error

	for _, path := range files {
		if ctx.Err() != nil {
			stopErr = ctx.Err()
			break
		}
		n, ferr := dr.drainFile(ctx, path, st)
		total += n
		if ferr == nil {
			continue
		}
		if errors.Is(ferr, context.Canceled) || errors.Is(ferr, context.DeadlineExceeded) {
			stopErr = ferr
			break
		}
		if dr.cfg.Metrics != nil {
			dr.cfg.Metrics.Counter(counterDrainFileError).Add(1)
		}
		dr.cfg.Log.Warn("daemon: drain: file error", "path", path, "err", ferr)
	}

	if serr := dr.saveState(st); serr != nil {
		dr.cfg.Log.Warn("daemon: drain: failed to persist state", "err", serr)
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
		return 0, nil
	}

	f, err := os.Open(paths.Long(path))
	if err != nil {
		return 0, err
	}
	// f is closed explicitly, below, before a possible os.Remove — not deferred to function
	// return: on Windows, deleting a file with a still-open handle fails, and a deferred Close
	// would not have run yet at the point this function calls os.Remove.

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
		offset += int64(len(raw))
		fs.Offset = offset

		line := bytes.TrimSuffix(raw, []byte{'\n'})
		if len(bytes.TrimSpace(line)) == 0 {
			continue // lenient to blank lines, though the writer never emits them
		}

		req, decErr := ipc.DecodeRequest(line)
		if decErr != nil {
			if dr.cfg.Metrics != nil {
				dr.cfg.Metrics.Counter(counterDrainFileError).Add(1)
			}
			dr.cfg.Log.Warn("daemon: drain: corrupt line", "path", path, "err", decErr)
			continue
		}

		key := core.HashBytes(walHashDomain, line)
		if dr.cfg.Seen != nil && dr.cfg.Seen.SeenOrAdd(key) {
			continue
		}

		req = resolveBlob(dr.cfg.Root, dr.cfg.Log, req)
		dctx, cancel := context.WithTimeout(ctx, drainLineDeadline)
		// The response is intentionally discarded and the offset advances unconditionally: the
		// spec's algorithm (task-3-spec.md drain.go step 3) treats "dispatched" as consumed even
		// when the handler itself refuses or times out. That is an accepted, deliberate loss at
		// this layer — the alternative (retrying forever) would let one permanently-failing line
		// wedge the whole file — but it is worth stating plainly given how much of the rest of this
		// package is about never losing data.
		dr.cfg.Dispatch(dctx, req)
		cancel()
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

	fs.Done = true
	// Persisted here — per file, on EOF, before the remove — not just once at the end of Drain
	// (task-3-spec.md drain.go step 4's exact ordering): a crash between this file's removal and
	// the end of the outer loop must not lose this file's recorded completion, which is what makes
	// "Drain is idempotent and resumable" true across a crash, not only across a clean cancel.
	if serr := dr.saveState(st); serr != nil {
		dr.cfg.Log.Warn("daemon: drain: failed to persist state", "err", serr)
	}
	if dr.shouldDelete(base) {
		if err := os.Remove(paths.Long(path)); err != nil {
			if !os.IsNotExist(err) {
				dr.cfg.Log.Warn("daemon: drain: failed to remove drained file", "path", path, "err", err)
			}
		} else {
			delete(st, base)
		}
	}
	return count, nil
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

// loadState reads state/drain.json, falling back to an empty state for a missing or corrupt file
// — the first-ever drain, or a state file this build can no longer parse, both start clean rather
// than error out.
func (dr *drainer) loadState() drainState {
	b, err := os.ReadFile(paths.Long(drainStatePath(dr.cfg.Root)))
	if err != nil {
		return drainState{}
	}
	var st drainState
	if err := json.Unmarshal(b, &st); err != nil || st == nil {
		return drainState{}
	}
	return st
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

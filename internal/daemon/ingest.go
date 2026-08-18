package daemon

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/obs"
	"github.com/qompack/qompack/internal/paths"
)

// ringCapacity is the in-memory queue depth between Accept and the worker pool: not a config
// default, so it lives here rather than internal/config/defaults.go (§2.4 B-C backpressure).
const ringCapacity = 4096 //nomagic:allow in-memory queue depth, not a config default (§2.4 B-C backpressure)

// walRotateBytes bounds one WAL segment before Accept rolls over to wal-<session>.<n>.ndjson.
const walRotateBytes = 64 << 20 // 64 MiB

// seenCapacity bounds seenSet's FIFO dedup window.
const seenCapacity = 65536

// walHashDomain is the dedup key domain for WAL/spool lines (task-3-spec.md ingest.go): key =
// core.HashBytes(walHashDomain, line).
const walHashDomain = "qompack.wal.v1"

// defaultWorkerCount is Start's fallback worker-pool size when the caller passes workers <= 0.
func defaultWorkerCount() int {
	n := runtime.NumCPU() / 2
	if n < 2 {
		n = 2
	}
	return n
}

// job is one accepted request queued for asynchronous (B-C) processing.
type job struct {
	req  ipc.Request
	recv core.UnixMilli
	key  core.Hash
}

// walFile is one session's cached WAL append handle plus its rotation bookkeeping.
type walFile struct {
	w     *os.File
	bytes int64
	seq   int
}

// ingest is the daemon's WAL-backed ingest queue (task-3-spec.md ingest.go): Accept is the B-B
// path (WAL append, then a non-blocking ring enqueue), and the worker pool started by Start drains
// the ring into run, timed into the B-C histogram.
type ingest struct {
	root string
	cfg  config.Config
	log  logging.Logger
	m    obs.Registry
	clk  core.Clock

	spoolDir string

	// histBB and histBC are obs.Budgets()'s histogram names for BB/BC, resolved once at
	// construction rather than on every Accept/dispatch call: obs.Budgets() builds a fresh
	// 6-element slice with 6 closures on every call, and re-deriving that on the hot path (once
	// per Accept, once per dispatch) is pure waste once there is a real caller.
	histBB string
	histBC string

	mu   sync.Mutex
	wals map[core.SessionID]*walFile

	ring chan job
	seen *seenSet

	wg sync.WaitGroup
}

// newIngest constructs an ingest queue rooted at root. Construction touches no filesystem state.
func newIngest(root string, cfg config.Config, log logging.Logger, m obs.Registry, clk core.Clock) *ingest {
	if log == nil {
		log = logging.Nop()
	}
	if clk == nil {
		clk = core.SystemClock()
	}

	return &ingest{
		root:     root,
		cfg:      cfg,
		log:      log,
		m:        m,
		clk:      clk,
		spoolDir: paths.Of(root).Spool,
		histBB:   histName(obs.BB),
		histBC:   histName(obs.BC),
		wals:     map[core.SessionID]*walFile{},
		ring:     make(chan job, ringCapacity),
		seen:     newSeenSet(seenCapacity),
	}
}

// Accept is the B-B path (task-3-spec.md ingest.go): append line — the exact bytes received — to
// the session's WAL, then enqueue a job for asynchronous processing without ever blocking, both
// timed end to end into the B-B histogram. A full ring is dropped rather than blocked on: the WAL
// append two lines earlier already made the line durable (it is what Drain reads on the next
// idle tick), so there is nothing left for a second, redundant copy to protect against losing —
// only l0_ring_full is counted, and Accept still returns immediately.
//
// The caller (the server's registered ipc.Handler) writes the ACK only after Accept returns and
// before any worker touches the job — that ordering is what makes the WAL the durability boundary
// (§2.4): a daemon crash after this call costs freshness, never data.
func (i *ingest) Accept(req ipc.Request, line []byte) error {
	// The wire path hands over ipc.EncodeRequest's output, which json.Encoder has already
	// terminated with '\n'; appendWAL adds the one terminator the WAL owns. Trimming here rather
	// than trusting the caller keeps two invariants at once: the WAL holds no blank separator
	// lines, and the dedup key below is computed over exactly the bytes Drain will hash when it
	// reads the line back out (drainFile trims the terminator before hashing). Before this trim,
	// the two sides hashed different bytes and every live-dispatched line was re-dispatched by
	// the SessionEnd flush drain.
	line = bytes.TrimSuffix(line, []byte{'\n'})
	work := func() error {
		if err := i.appendWAL(req.Session, line); err != nil {
			return err
		}
		j := job{
			req:  req,
			recv: core.NowMilli(i.clk),
			key:  core.HashBytes(walHashDomain, line),
		}
		select {
		case i.ring <- j:
		default:
			if i.m != nil {
				i.m.Counter(counterL0RingFull).Add(1)
			}
		}
		return nil
	}

	var err error
	if i.m != nil {
		err = obs.Timed(i.m.Hist(i.histBB), work)
	} else {
		err = work()
	}
	if err != nil {
		return fmt.Errorf("daemon: ingest: accept: %w", err)
	}
	return nil
}

// appendWAL writes line, plus exactly one trailing newline, to req's session WAL file, opening
// (and caching) the handle on first use and rotating past walRotateBytes. No fsync (§2.4: "O_APPEND,
// no fsync").
func (i *ingest) appendWAL(sess core.SessionID, line []byte) error {
	i.mu.Lock()
	defer i.mu.Unlock()

	wf, ok := i.wals[sess]
	if !ok {
		wf = &walFile{}
		i.wals[sess] = wf
	}
	if wf.w == nil {
		if err := i.openWALLocked(sess, wf); err != nil {
			return err
		}
	}

	buf := make([]byte, 0, len(line)+1)
	buf = append(buf, line...)
	buf = append(buf, '\n')

	if wf.bytes > 0 && wf.bytes+int64(len(buf)) > walRotateBytes {
		if err := wf.w.Close(); err != nil {
			return err
		}
		wf.w = nil
		wf.seq++
		if err := i.openWALLocked(sess, wf); err != nil {
			return err
		}
	}

	n, err := wf.w.Write(buf)
	if err != nil {
		return err
	}
	wf.bytes += int64(n)
	return nil
}

// openWALLocked opens (creating if needed) the WAL segment file for sess at wf's current
// rotation sequence. mu must be held.
func (i *ingest) openWALLocked(sess core.SessionID, wf *walFile) error {
	if err := os.MkdirAll(paths.Long(i.spoolDir), 0o700); err != nil {
		return fmt.Errorf("daemon: ingest: mkdir spool: %w", err)
	}
	p := walPath(i.spoolDir, sess, wf.seq)
	wc, err := paths.AppendOnly(p)
	if err != nil {
		return fmt.Errorf("daemon: ingest: open wal: %w", err)
	}
	f, ok := wc.(*os.File)
	if !ok {
		return fmt.Errorf("daemon: ingest: AppendOnly returned a non-*os.File writer")
	}
	wf.w = f
	wf.bytes = 0
	if fi, statErr := f.Stat(); statErr == nil {
		wf.bytes = fi.Size() // resume the running byte count across daemon restarts
	}
	return nil
}

// walPath returns the WAL file path for sess at rotation seq: wal-<session>.ndjson for seq 0,
// wal-<session>.<seq>.ndjson thereafter.
func walPath(spoolDir string, sess core.SessionID, seq int) string {
	if seq == 0 {
		return filepath.Join(spoolDir, fmt.Sprintf("wal-%s.ndjson", sess))
	}
	return filepath.Join(spoolDir, fmt.Sprintf("wal-%s.%d.ndjson", sess, seq))
}

// Start launches workers goroutines (or defaultWorkerCount() if workers <= 0), each pulling jobs
// off the ring until ctx is done, deduplicating against seen, and dispatching to run, timed into
// the B-C histogram. A panic inside run is recovered, counted and Loud'd — it never brings down
// the worker.
func (i *ingest) Start(ctx context.Context, workers int, run func(context.Context, ipc.Request)) {
	if workers <= 0 {
		workers = defaultWorkerCount()
	}
	for w := 0; w < workers; w++ {
		i.wg.Add(1)
		go i.worker(ctx, run)
	}
}

func (i *ingest) worker(ctx context.Context, run func(context.Context, ipc.Request)) {
	defer i.wg.Done()
	for {
		select {
		case <-ctx.Done():
			return
		case j, ok := <-i.ring:
			if !ok {
				return
			}
			i.dispatch(ctx, run, j)
		}
	}
}

// dispatch resolves any blob descriptor in j.req.Raw before calling run — ipc.Client externalizes
// on the live wire path (client.go's Send), not only the spool fallback, so any request whose
// encoded line reached ExternalizeThreshold arrives here still carrying a {"blob":...} descriptor
// in place of Event.ToolResponse. The dedup key (j.key) was computed in Accept from the WAL line
// with its terminator trimmed, before any resolution — the same bytes Drain hashes when it later reads the
// same line back out of the WAL — so a request resolved here and the identical (still-descriptor)
// bytes Drain might independently see cannot double-dispatch: whichever side's SeenOrAdd runs
// first wins, and only one of them ever reaches run.
func (i *ingest) dispatch(ctx context.Context, run func(context.Context, ipc.Request), j job) {
	defer func() {
		if r := recover(); r != nil {
			if i.m != nil {
				i.m.Counter(counterL0WorkerPanic).Add(1)
			}
			i.log.Loud("daemon: ingest worker panicked — job dropped", "op", string(j.req.Op), "recover", r)
		}
	}()

	if i.seen.SeenOrAdd(j.key) {
		return
	}

	req := resolveBlob(i.root, i.log, j.req)
	work := func() error { run(ctx, req); return nil }
	if i.m != nil {
		_ = obs.Timed(i.m.Hist(i.histBC), work)
	} else {
		_ = work()
	}
}

// CloseSession closes and forgets sess's cached WAL handle, releasing the file descriptor once a
// session has ended. It is a no-op for a session that was never opened.
func (i *ingest) CloseSession(sess core.SessionID) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	wf, ok := i.wals[sess]
	if !ok {
		return nil
	}
	delete(i.wals, sess)
	if wf.w == nil {
		return nil
	}
	return wf.w.Close()
}

// Wait blocks until every worker goroutine Start launched has returned — which happens once ctx
// (the ctx Start was given) is done and each worker's in-flight dispatch, if any, finishes. Task 4
// uses this to join the worker pool before tearing down the store on shutdown: without it, a
// worker can still be mid-run when the rest of the daemon's dependencies are closed out from under
// it.
func (i *ingest) Wait() { i.wg.Wait() }

// Close closes every cached WAL handle. It does not stop the worker pool — that is ctx's job, via
// Start — and does not wait for workers to finish; call Wait for that.
func (i *ingest) Close() error {
	i.mu.Lock()
	defer i.mu.Unlock()

	var firstErr error
	for sess, wf := range i.wals {
		if wf.w != nil {
			if err := wf.w.Close(); err != nil && firstErr == nil {
				firstErr = err
			}
		}
		delete(i.wals, sess)
	}
	return firstErr
}

// seenSet is a capacity-bounded FIFO of dedup keys (task-3-spec.md ingest.go): it makes both the
// worker pool and Drain idempotent within a daemon lifetime when the same instance is shared
// between them.
type seenSet struct {
	mu       sync.Mutex
	capacity int
	set      map[core.Hash]struct{}
	order    []core.Hash
}

// newSeenSet returns an empty seenSet bounded at capacity entries.
func newSeenSet(capacity int) *seenSet {
	return &seenSet{capacity: capacity, set: make(map[core.Hash]struct{}, capacity)}
}

// SeenOrAdd reports whether key has already been recorded; if not, it records it (evicting the
// oldest entry first if the set is at capacity) and returns false.
func (s *seenSet) SeenOrAdd(key core.Hash) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.set[key]; ok {
		return true
	}
	if len(s.order) >= s.capacity {
		oldest := s.order[0]
		s.order = s.order[1:]
		delete(s.set, oldest)
	}
	s.set[key] = struct{}{}
	s.order = append(s.order, key)
	return false
}

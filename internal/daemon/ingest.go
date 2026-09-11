package daemon

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/obs"
	"github.com/qompack/qompack/internal/observer"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/store"
)

// ringCapacity is the in-memory queue depth between Accept and the worker pool: not a config
// default, so it lives here rather than internal/config/defaults.go (§2.4 B-C backpressure).
const ringCapacity = 4096 //nomagic:allow in-memory queue depth, not a config default (§2.4 B-C backpressure)

// walRotateBytes bounds one WAL segment before Accept rolls over to wal-<session>.<n>.ndjson.
const walRotateBytes = 64 << 20 // 64 MiB

// seenCapacity bounds seenSet's FIFO dedup window.
const seenCapacity = 65536

// walHashDomain is the dedup key domain for WAL/spool lines (task-3-spec.md ingest.go): key =
// core.HashBytes(walHashDomain, line). It survives only as the FALLBACK identity for a delivery
// that has no nonce and therefore no lease — see deliveryIdentityDomain.
const walHashDomain = "qompack.wal.v1"

// deliveryIdentityDomain keys the dedup set on the durable observation identity rather than on the
// content of the wire line (invariant 2). Two deliveries whose bytes are identical are two
// deliveries: they carry two nonces, take two leases, get two ObservationIDs and therefore two
// distinct keys. The content hash is still computed and still used — by store.PutBytes, for
// near-duplicate detection, which is a question about content and not about identity.
const deliveryIdentityDomain = "qompack.delivery.identity.v1"

// deliveryRequestDomain binds a lease to the request it was taken for. The binding must survive
// every representation of the SAME delivery: the daemon's own WAL re-encoding, the client spool
// fallback, and ipc.Client.externalize replacing an oversize tool response with a blob descriptor.
// So it covers the fields that identify the invocation and cannot be rewritten by any of those —
// op, session and the host timestamp minted once per hook — and deliberately not the payload bytes,
// which differ between representations of one delivery and would turn a legitimate retry into an
// append-only violation.
const deliveryRequestDomain = "qompack.delivery.request.v1"

// deliveryRequestSep separates the three fields so no two field values can run together.
const deliveryRequestSep = "\x00"

// deliveryRequestHash is the lease binding described at deliveryRequestDomain.
func deliveryRequestHash(req ipc.Request) core.Hash {
	return core.HashBytes(deliveryRequestDomain,
		[]byte(string(req.Op)+deliveryRequestSep+string(req.Session)+deliveryRequestSep+strconv.FormatInt(int64(req.TS), 10)))
}

// deliveryIdentityKey is the dedup key for one delivery: its durable identity when it has one, and
// the content hash of its wire line when it does not.
func deliveryIdentityKey(lease deliveryLease, leased bool, line []byte) core.Hash {
	if leased {
		return core.HashBytes(deliveryIdentityDomain, []byte(lease.ObservationID))
	}
	return core.HashBytes(walHashDomain, line)
}

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
	// lease is the durable delivery assignment made in Accept: the ObservationID this delivery
	// keeps across retries, restarts and the spool fallback. leased is false only when no journal
	// was available or the delivery carried no nonce, which is a gap, not an identity.
	lease  deliveryLease
	leased bool
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

	// mu guards wals and every segment handle in it. A WAL batch holds it from its first line to
	// its last Sync (commitWALBatch), so rotation, CloseSession and Close never touch a handle that
	// a batch is writing or syncing.
	mu   sync.Mutex
	wals map[core.SessionID]*walFile

	// walQ group-commits the WAL (design §2.4): appendWAL enqueues its line and waits, and the
	// caller that leads a batch writes and syncs every line queued behind it.
	walQ groupQueue[*walItem]

	// writeWAL and syncWAL are the WAL's two I/O calls on a segment handle: (*os.File).Write and
	// (*os.File).Sync in production. They are fields so that tests can observe, block and fail
	// them per segment; nothing else sets them.
	writeWAL func(*os.File, []byte) (int, error)
	syncWAL  func(*os.File) error

	ring chan job
	seen *seenSet

	// journal resolves the daemon's held delivery journal. It is a function rather than a field
	// because the journal belongs to the singleton Lock, which Run acquires after the ingest queue
	// is constructed and releases before it is torn down. A nil journal (or one that answers an
	// error) is a recorded gap: the delivery still reaches the WAL, it simply has no durable
	// identity, and nothing downstream may pretend otherwise.
	journal func() (*deliveryJournal, error)

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
		walQ:     groupQueue[*walItem]{maxN: groupCommitMaxRequests, maxBytes: walGroupCommitMaxBytes, size: walItemSize},
		writeWAL: (*os.File).Write,
		syncWAL:  (*os.File).Sync,
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
//
// DURABILITY POINTS ON THIS PATH — audited, three, and all three are required. Accept carried one
// before delivery identity became durable, and it is the operation the p99 budget is measured
// against, so the count is stated here rather than left to be rediscovered:
//
//  1. The WAL segment fsync (appendWAL). Guarantees the exact received bytes are on disk before
//     the transport acknowledgement goes back to the hook. It is §2.4's boundary itself: the ACK
//     is a promise that the delivery survives a crash, and without this sync the promise is a
//     page-cache guess. Nothing else on this path holds these bytes. It is group-committed:
//     Accepts that arrive together share one Write and one fsync per WAL segment, and appendWAL
//     returns nil only once the fsync covering this delivery's own line has returned.
//
//  2. The delivery-lease journal fsync (deliveryJournal.lease). Guarantees the nonce -> arrival ->
//     ObservationID assignment is durable BEFORE the identity is handed to a job — the point after
//     which a redelivery must recover the same identity rather than mint a second one (invariant
//     2). It cannot be merged with (1): fsync is per file, and these are two files in two trees.
//     It cannot be dropped either, because the journal refuses to open on a torn tail, so an
//     unsynced line turns a machine crash into a whole-journal degradation rather than a lost row.
//     It is group-committed: leases that arrive together share one Write and one fsync
//     (commitLeases), and a lease returns only once the batch carrying its line is sealed.
//
//  3. The lease position sidecar (deliveryJournal.savePosition, via paths.WriteAtomic: a temp-file
//     fsync plus a parent-directory fsync — one durability point, two syscalls). Guarantees the
//     sealed frontier — byte count, record count and hash chain — that recovery validates the
//     journal against. It is what detects a TRUNCATED journal: load refuses a file shorter than
//     the sealed prefix, and a lost assignment that went undetected would let a redelivery of an
//     already-published delivery take a second identity. It is strictly ORDERED after (2): a
//     position ahead of its file poisons the journal permanently, so the two syncs are a sequence,
//     not a pair that could share one. It is written once per lease batch, after that batch's
//     fsync.
//
// What must not be done is to RELEASE an identity — admit it to the journal's maps, hand it to a
// job, let Accept return — before the seal that covers it is durable. That opens a window in which
// a truncated tail is invisible, and that window is not a latency cost, it is silent identity
// loss. A lease batch releases nothing before its seal returns, so the set of released but
// unsealed identities is empty at every instant, as it was with one seal per lease. What grows
// with the batch is only the set of lines that are durable, unsealed and never released after a
// crash between the fsync and the seal: no caller ever saw those identities, and the next open
// re-seals such a complete tail before anyone can reuse it (SP20-D1 design, section 2.11).
// Deferring (2) and (3) off Accept entirely (to just before publication in dispatch, which is B-C
// and not the p99 budget) would release before the seal, and it would also rebuild the journal's
// synced-bytes-only admission rule, which is what makes a concurrent redelivery of the same nonce
// see the first lease at all, so it is not done.
//
// So: 3 durability points per accepted leased delivery, and 4 fsync syscalls for one that has its
// WAL batch and its lease batch to itself; deliveries that arrive together share (1)'s fsync per
// segment, and (2) and (3) per lease batch. A delivery with no nonce, or one whose journal is
// unavailable, is an unleased gap and pays only (1).
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
		// Identity is assigned here, before the job is queued and therefore before anything can
		// process it. A redelivery of the same nonce — the client's spool fallback, a drained WAL
		// line after restart — takes the same lease back unchanged and reuses this identity.
		lease, leased := i.leaseDelivery(context.Background(), req)
		j := job{
			req:    req,
			recv:   core.NowMilli(i.clk),
			key:    deliveryIdentityKey(lease, leased, line),
			lease:  lease,
			leased: leased,
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

// walItem is one WAL append's request in walQ (design §2.4).
type walItem struct {
	sess core.SessionID
	// line is the trimmed line; the batch adds the one terminator the WAL owns.
	line []byte
	// err is the append's result. It starts as errNotCommitted and becomes nil only once the Sync
	// covering line has returned (J-A2); a failure on the way replaces it with that failure.
	err error
}

// walItemSize is a request's exact size in a WAL batch: its line and its terminator.
func walItemSize(it *walItem) int { return len(it.line) + 1 }

// appendWAL makes line, plus exactly one trailing newline, durable in sess's WAL segment, opening
// (and caching) the handle on first use and rotating past walRotateBytes. It returns nil only once
// a Sync that covered that line has returned: Sync precedes the transport acknowledgement, which
// corrects the historical no-fsync boundary. Full object and reference publication is a separate
// SP-20 gate.
//
// It is enqueue-and-wait on walQ. A caller that finds no batch in flight commits one
// (commitWALBatch) inline on its own goroutine, and every caller that arrives meanwhile waits for
// the batch that carries its line. So an isolated append pays exactly one Write and one Sync, as it
// always did, and appends that arrive together share one of each per segment.
func (i *ingest) appendWAL(sess core.SessionID, line []byte) error {
	it := &walItem{sess: sess, line: line, err: errNotCommitted}
	i.walQ.run(it, i.commitWALBatch)
	return it.err
}

// walSegment is one segment file's share of a WAL batch.
type walSegment struct {
	// f is the handle buf is written to, bound when the segment's first line is buffered. It is
	// never re-read from wf: a rotation later in the same batch replaces wf.w, and a line belongs to
	// the file its rotation decision counted it against (J-A7).
	f  *os.File
	wf *walFile
	// buf holds the segment's lines, each with its terminator, in queue order; items holds their
	// requests in the same order.
	buf   []byte
	items []*walItem
	// flushed is set once a rotation has written and synced buf ahead of closing f.
	flushed bool
	// err is the segment's write or sync failure, shared by every request in items.
	err error
}

// commitWALBatch is walQ's commit: it appends one batch of lines, in queue order, and resolves each
// line only once the Sync covering it has returned.
//
// It holds mu for the whole batch, as one append did. Each line goes to its session's open segment,
// rotating first with exactly a sequential append's accounting — the bytes the segment already
// holds plus the bytes this batch has buffered for it — so the lines, their single terminators and
// the segment boundaries are byte-identical to one append per line. Then comes one Write per
// segment the batch buffered lines for, then one Sync per segment written, in parallel when there
// is more than one, and only after every Sync has returned does any line's result change.
//
// A rotation writes the outgoing segment's buffered lines to that segment's own handle and syncs
// them BEFORE it closes the handle: they are not durable yet, and nothing could sync them once the
// handle is closed. A line from an earlier batch needs no such sync; its own batch synced it.
//
// Failure is per segment. A write error, a short write or a sync error fails every line buffered
// for that segment and no other. A failed line's result is final when this batch returns, so no
// later Sync of the same file can turn it into an acknowledgement.
func (i *ingest) commitWALBatch(batch []*walItem) {
	i.mu.Lock()
	defer i.mu.Unlock()

	var segs []*walSegment             // every segment this batch buffered lines for, in first-use order
	open := map[*walFile]*walSegment{} // the segment each session's next line would join
	for _, it := range batch {
		wf, err := i.walForLocked(it.sess)
		if err != nil {
			it.err = err
			continue
		}
		s := open[wf]
		size := wf.bytes
		if s != nil {
			size += int64(len(s.buf))
		}
		if size > 0 && size+int64(walItemSize(it)) > walRotateBytes {
			if s != nil {
				i.writeWALSegment(s)
				if s.err == nil {
					s.err = i.syncWAL(s.f)
				}
				s.flushed = true
				delete(open, wf)
			}
			if err := i.rotateWALLocked(it.sess, wf); err != nil {
				it.err = err
				continue
			}
			s = nil
		}
		if s == nil {
			s = &walSegment{f: wf.w, wf: wf}
			open[wf] = s
			segs = append(segs, s)
		}
		s.buf = append(s.buf, it.line...)
		s.buf = append(s.buf, '\n')
		s.items = append(s.items, it)
	}

	var written []*walSegment
	for _, s := range segs {
		if s.flushed {
			continue
		}
		i.writeWALSegment(s)
		if s.err == nil {
			written = append(written, s)
		}
	}
	i.syncWALSegments(written)

	for _, s := range segs {
		for _, it := range s.items {
			it.err = s.err
		}
	}
}

// writeWALSegment writes s's buffered lines to s's own handle in one Write, counting the bytes into
// the walFile exactly as a sequential append did: nothing on a write error, and whatever was written
// on a short write, which then fails the segment with io.ErrShortWrite.
func (i *ingest) writeWALSegment(s *walSegment) {
	n, err := i.writeWAL(s.f, s.buf)
	if err != nil {
		s.err = err
		return
	}
	s.wf.bytes += int64(n)
	if n != len(s.buf) {
		s.err = io.ErrShortWrite
	}
}

// syncWALSegments syncs each segment in segs once and returns when every Sync has returned. With
// more than one, the leader syncs the first itself and each of the others on a goroutine of its own
// (fsync is per file, and a batch's segments belong to unrelated sessions), then joins them all. A
// panic in any of them is re-raised on the leader's goroutine after the join, so no Sync outlives
// its batch and none can take the process down from a goroutine that nothing recovers.
func (i *ingest) syncWALSegments(segs []*walSegment) {
	switch len(segs) {
	case 0:
		return
	case 1:
		segs[0].err = i.syncWAL(segs[0].f)
		return
	}
	panics := make([]any, len(segs))
	syncOne := func(k int) {
		defer func() { panics[k] = recover() }()
		segs[k].err = i.syncWAL(segs[k].f)
	}
	var wg sync.WaitGroup
	for k := 1; k < len(segs); k++ {
		wg.Go(func() { syncOne(k) })
	}
	syncOne(0)
	wg.Wait()
	for _, p := range panics {
		if p != nil {
			panic(p)
		}
	}
}

// walForLocked returns sess's cached WAL handle, opening its segment at the current rotation
// sequence when it has none; a failed open leaves it with none, so the session's next line retries.
// mu must be held.
func (i *ingest) walForLocked(sess core.SessionID) (*walFile, error) {
	wf, ok := i.wals[sess]
	if !ok {
		wf = &walFile{}
		i.wals[sess] = wf
	}
	if wf.w == nil {
		if err := i.openWALLocked(sess, wf); err != nil {
			return nil, err
		}
	}
	return wf, nil
}

// rotateWALLocked closes wf's segment and opens the next, exactly as a sequential append did: a Close
// that fails leaves wf on its closed handle, and an open that fails leaves it with none. mu must be
// held, and every line a batch buffered for the closing segment must already have been through its
// Write and its Sync, whatever their outcome.
func (i *ingest) rotateWALLocked(sess core.SessionID, wf *walFile) error {
	if err := wf.w.Close(); err != nil {
		return err
	}
	wf.w = nil
	wf.seq++
	return i.openWALLocked(sess, wf)
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
func (i *ingest) Start(ctx context.Context, workers int, run func(context.Context, ipc.Request) ipc.Response) {
	if workers <= 0 {
		workers = defaultWorkerCount()
	}
	for w := 0; w < workers; w++ {
		i.wg.Add(1)
		go i.worker(ctx, run)
	}
}

func (i *ingest) worker(ctx context.Context, run func(context.Context, ipc.Request) ipc.Response) {
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
// bytes Drain might independently see share same-process ownership. Only a successful handler
// acknowledgement enters the bounded completed set; rejection remains retryable. Restart does
// not retain this set, so handlers must tolerate at-least-once delivery.
func (i *ingest) dispatch(ctx context.Context, run func(context.Context, ipc.Request) ipc.Response, j job) {
	defer func() {
		if r := recover(); r != nil {
			if i.m != nil {
				i.m.Counter(counterL0WorkerPanic).Add(1)
			}
			i.log.Loud("daemon: ingest worker panicked; WAL retained for retry", "op", string(j.req.Op))
		}
	}()

	_, acquired := i.seen.begin(j.key)
	if !acquired {
		return
	}
	acknowledged := false
	defer func() { i.seen.finish(j.key, acknowledged) }()

	req, _, err := readBlob(i.root, j.req)
	if err != nil {
		i.log.Warn("daemon: ingest blob unavailable; WAL retained for retry", "op", string(j.req.Op))
		return // retain the WAL and blob for recovery
	}
	work := func() error {
		// Publication order, stage 1: the durable object. A capture that cannot be made durable
		// blocks the reference and the frontier behind it; the delivery stays retryable and the
		// host's own result is untouched either way (invariant 4).
		if j.leased {
			if err := publishCapture(i.root, req, j.lease); err != nil {
				if i.m != nil {
					i.m.Counter(counterSidecarFailed).Add(1)
				}
				i.log.Warn("daemon: capture not durable; publication blocked", "op", string(j.req.Op), "err", err)
				return nil
			}
		}
		// Stage 2: the verified reference, written by the bound observer inside run. The identity
		// travels on the context so the host'''s own payload type never has to carry it.
		resp := run(observer.WithObservation(ctx, j.lease.ObservationID), req)
		acknowledged = resp.OK && resp.Err == ""
		if !acknowledged {
			i.log.Warn("daemon: ingest handler did not acknowledge; WAL retained for retry", "op", string(j.req.Op))
			return nil
		}
		// Stage 3: the committed frontier. Until this record exists the delivery is not published,
		// no matter what the handler returned — Response.OK is an in-memory answer and does not
		// survive the restart the frontier exists to be read after.
		if err := i.commitDelivery(ctx, j, core.Hash{}); err != nil {
			acknowledged = false
			if i.m != nil {
				i.m.Counter(counterDeliveryAckFailed).Add(1)
			}
			i.log.Warn("daemon: delivery not acknowledged; WAL retained for retry", "op", string(j.req.Op), "err", err)
		}
		// The WAL still names any externalized blob. Only Drain's persisted offset may release
		// it; an in-memory success is lost on restart and is not a durable acknowledgement.
		return nil
	}
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

// removeDrainedWAL removes the WAL segment at path on the drainer's behalf and reports whether it
// did. It refuses, without error, a segment this ingest holds open for appending — a session's
// current segment, where its next append lands — and one whose size no longer equals drained, the
// offset the drain consumed. Removing a held segment failed on Windows with a sharing violation on
// every drain pass; on POSIX the unlink succeeded, and every later append, fsynced and ACKed as
// durable, went into an unlinked inode that a crash then lost for good. The session's liveness
// cannot stand in for this check: the registry does not know every session whose segment is open
// (one SessionEnd ended, then a straggler reopened; one EndAbandoned ended while its handle stayed
// cached), and it is not the lock the appends take.
//
// Both refusals are decided under i.mu, which every append and every segment open takes, so no
// append can land between the decision and the unlink. An append that comes after an unlink
// reopens the name as a fresh segment, which the next drain reads from offset zero. Only the base
// name is compared: the ingest writes nowhere but its own spool directory, and a false "held" only
// defers a removal. The mutex is held across one stat and one unlink, so an Accept arriving in that
// moment waits for them; that happens once per segment actually retired, and a held segment costs no
// I/O at all.
func (i *ingest) removeDrainedWAL(path string, drained int64) (bool, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.holdsLocked(filepath.Base(path)) {
		return false, nil
	}
	return removeIfUnchanged(path, drained)
}

// holdsWAL reports whether this ingest holds the segment at path open for appending: the first of
// removeDrainedWAL's two refusals, answered alone and under the same mutex. The drainer asks it
// before it forgets a finished segment's progress (DrainConfig.HoldsWAL), so a segment held all
// along is not forgotten, refused and restored on every pass. The answer can be stale by the time
// the drainer acts on it, which is why removeDrainedWAL decides again.
func (i *ingest) holdsWAL(path string) bool {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.holdsLocked(filepath.Base(path))
}

// holdsLocked reports whether base names the current segment of a session this ingest holds open.
// Only the base name is compared (removeDrainedWAL says why). i.mu must be held.
func (i *ingest) holdsLocked(base string) bool {
	for sess, wf := range i.wals {
		if wf.w != nil && filepath.Base(walPath(i.spoolDir, sess, wf.seq)) == base {
			return true
		}
	}
	return false
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
	working  map[core.Hash]struct{}
}

// newSeenSet returns an empty seenSet bounded at capacity entries.
func newSeenSet(capacity int) *seenSet {
	return &seenSet{capacity: capacity, set: make(map[core.Hash]struct{}, capacity), working: make(map[core.Hash]struct{})}
}

// begin distinguishes a completed delivery from one still owned by another handler. A drainer
// must not consume a line merely because an ingest worker is currently handling it.
func (s *seenSet) begin(key core.Hash) (completed, acquired bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.set[key]; ok {
		return true, false
	}
	if _, ok := s.working[key]; ok {
		return false, false
	}
	s.working[key] = struct{}{}
	return false, true
}

func (s *seenSet) finish(key core.Hash, acknowledged bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.working, key)
	if acknowledged {
		s.addLocked(key)
	}
}

// SeenOrAdd reports whether key has already been recorded; if not, it records it (evicting the
// oldest entry first if the set is at capacity) and returns false.
func (s *seenSet) SeenOrAdd(key core.Hash) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.set[key]; ok {
		return true
	}
	s.addLocked(key)
	return false
}

func (s *seenSet) addLocked(key core.Hash) {
	if s.capacity <= 0 {
		return
	}
	if len(s.order) >= s.capacity {
		oldest := s.order[0]
		s.order = s.order[1:]
		delete(s.set, oldest)
	}
	s.set[key] = struct{}{}
	s.order = append(s.order, key)
}

// ---------------------------------------------------------------------------
// Delivery identity, durable capture and committed frontier (T20-M1-01..03)

// leaseDelivery takes (or re-takes) the durable assignment for req. A delivery with no nonce and a
// journal that cannot be opened both answer false, which is a GAP and is counted as one: the record
// is still handled, but nothing downstream may claim it has a durable identity.
func (i *ingest) leaseDelivery(ctx context.Context, req ipc.Request) (deliveryLease, bool) {
	if i.journal == nil || req.Nonce == "" {
		i.countUnleased()
		return deliveryLease{}, false
	}
	j, err := i.journal()
	if err != nil || j == nil {
		i.countUnleased()
		return deliveryLease{}, false
	}
	lease, err := j.lease(ctx, req.Nonce, req.Session, deliveryRequestHash(req))
	if err != nil {
		i.countUnleased()
		i.log.Warn("daemon: delivery lease unavailable; identity is a gap", "op", string(req.Op), "err", err)
		return deliveryLease{}, false
	}
	return lease, true
}

func (i *ingest) countUnleased() {
	if i.m != nil {
		i.m.Counter(counterDeliveryUnleased).Add(1)
	}
}

// publishCapture is the FIRST of publication order's three stages: the durable object. It persists
// the admitted host payload as a sidecar keyed by the delivery's observation identity, before any
// reference or frontier record exists. A failure here returns an error and the caller must not
// publish anything — a reference to a capture that is not durable is exactly the published handle
// to an unavailable dependency that restart must never find.
func publishCapture(root string, req ipc.Request, lease deliveryLease) error {
	sc := store.CaptureSidecar{
		ObservationID: lease.ObservationID,
		Session:       lease.Session,
		Arrival:       lease.ArrivalSeq,
		Op:            string(req.Op),
		TS:            req.TS,
		Delivery:      lease.Delivery,
		Admission:     admissionClient,
		HashVersion:   core.EvidenceHashVersion,
		Fidelity:      core.FidelityUnknown,
		Outcome:       core.OutcomeUnavailable,
		CaptureError:  core.CaptureErrorPolicy,
	}
	if c := req.Capture; c != nil {
		sc.SourceFormat, sc.PolicyVersion = c.SourceFormat, c.PolicyVersion
		sc.HashVersion, sc.Fidelity, sc.Outcome = c.HashVersion, c.Fidelity, c.Outcome
		sc.CaptureError, sc.Redacted, sc.Truncated = c.CaptureError, c.Redacted, c.Truncated
		sc.SourceBytes, sc.HostFields, sc.Bytes = c.SourceBytes, c.HostFields, c.Bytes
	}
	return store.WriteCaptureSidecar(root, sc)
}

// admissionClient labels a sidecar whose decision was made before the transport. The daemon-side
// gate relabels the ones it decided itself; see handlers.go admitDelivery.
const admissionClient = "client"

// commitDelivery is the LAST of publication order's three stages: the committed frontier. It runs
// only after a successful dispatch, and its failure un-acknowledges the delivery so the record that
// would let it be retried is never released.
func (i *ingest) commitDelivery(ctx context.Context, j job, root core.Hash) error {
	if !j.leased || i.journal == nil {
		return nil
	}
	jr, err := i.journal()
	if err != nil || jr == nil {
		return fmt.Errorf("daemon: ingest: delivery journal unavailable for acknowledgement")
	}
	return jr.acknowledge(ctx, j.lease.Delivery, j.lease.ObservationID, root)
}

package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
)

// The delivery-lease group-commit tests (design §6.2 T9–T15 and T12, lease side), adapted to step 1
// of the rollout: every seal is today's v1 position file, written by savePosition through
// paths.WriteAtomic, and checkFile is today's. They run the production lease on goroutines of their
// own, the way concurrent Accepts and the drainer call it, and watch the journal through its two
// seams: its writer (Write, Sync, Close) and sealLease. Every ordering they rely on is made by
// holding a Sync or a seal open and by the queue's own length. The clock only bounds waits that a
// correct build never reaches, so that a deadlock fails the test instead of hanging it.

// leaseEvent is one entry in a leaseProbe's log: a seam call, a lease that returned or panicked, an
// evaluation a leaseCtx saw, or a test's own note.
type leaseEvent struct {
	at   int
	kind string // "write", "sync", "seal", "sealed", "close", "eval", "return", "panic", or a test's own
	call int    // write, sync, seal, sealed: the call's 1-based index among the probe's calls of that kind
	id   int    // eval, return, panic: the request's id
	data []byte // write: the bytes the call reported written
	pos  deliveryPosition
	err  error
}

// leaseProbe instruments one journal's seams. It numbers every Write, Sync and seal, holds the
// first Sync on syncGate when there is one, hands each call to the test's hook in place of the real
// one when there is one, and logs how the call ended. The gate and the hooks are set before the
// first lease and never changed afterwards.
type leaseProbe struct {
	j        *deliveryJournal
	syncGate *walGate
	onWrite  func(call int, b []byte) (int, error)
	onSync   func(call int) error
	// onSeal runs in place of the seal; seal is the real one, for a hook that holds or wraps it.
	onSeal func(call int, seal func() error) error

	writes, syncs, seals atomic.Int32

	mu     sync.Mutex
	events []leaseEvent
}

func newLeaseProbe(j *deliveryJournal) *leaseProbe {
	p := &leaseProbe{j: j}
	f := j.file
	j.writer = leaseFaultWriter{
		file: f,
		write: func(b []byte) (int, error) {
			call := int(p.writes.Add(1))
			var n int
			var err error
			if p.onWrite != nil {
				n, err = p.onWrite(call, b)
			} else {
				n, err = f.Write(b)
			}
			p.add(leaseEvent{kind: "write", call: call, data: slices.Clone(b[:min(max(n, 0), len(b))]), err: err})
			return n, err
		},
		sync: func() error {
			call := int(p.syncs.Add(1))
			if call == 1 && p.syncGate != nil {
				p.syncGate.hold()
			}
			var err error
			if p.onSync != nil {
				err = p.onSync(call)
			} else {
				err = f.Sync()
			}
			p.add(leaseEvent{kind: "sync", call: call, err: err})
			return err
		},
		close: func() error {
			err := f.Close()
			p.add(leaseEvent{kind: "close", err: err})
			return err
		},
	}
	seal := j.sealLease
	j.sealLease = func(size int64, count int, chain core.Hash) error {
		call := int(p.seals.Add(1))
		pos := deliveryPosition{Version: core.EvidenceVersion, Bytes: size, Count: count, Chain: chain}
		p.add(leaseEvent{kind: "seal", call: call, pos: pos})
		sealNow := func() error { return seal(size, count, chain) }
		var err error
		if p.onSeal != nil {
			err = p.onSeal(call, sealNow)
		} else {
			err = sealNow()
		}
		p.add(leaseEvent{kind: "sealed", call: call, pos: pos, err: err})
		return err
	}
	return p
}

func (p *leaseProbe) add(e leaseEvent) {
	p.mu.Lock()
	defer p.mu.Unlock()
	e.at = len(p.events)
	p.events = append(p.events, e)
}

// log returns every event so far, in order.
func (p *leaseProbe) log() []leaseEvent {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.events)
}

// eventAt returns the index in log of the first event of kind whose id (eval, return, panic) or
// call (every other kind) is n, and fails when there is none.
func eventAt(t *testing.T, log []leaseEvent, kind string, n int) int {
	t.Helper()
	i := slices.IndexFunc(log, func(e leaseEvent) bool {
		if e.kind != kind {
			return false
		}
		switch kind {
		case "eval", "return", "panic":
			return e.id == n
		default:
			return e.call == n
		}
	})
	require.GreaterOrEqualf(t, i, 0, "no %s event %d in the log", kind, n)
	return i
}

// leaseCall is one lease request. A nil ctx is context.Background().
type leaseCall struct {
	id       int
	ctx      context.Context
	delivery string
	session  core.SessionID
	request  core.Hash
}

func (c leaseCall) context() context.Context {
	if c.ctx == nil {
		return context.Background()
	}
	return c.ctx
}

// leaseRun is one lease running on its own goroutine. lease, err and recovered are final once done
// is closed.
type leaseRun struct {
	leaseCall
	done      chan struct{}
	lease     deliveryLease
	err       error
	recovered any
}

// goLease runs c on a new goroutine and logs its return, or its panic, on p the moment it happens.
func goLease(p *leaseProbe, c leaseCall) *leaseRun {
	r := &leaseRun{leaseCall: c, done: make(chan struct{})}
	go func() {
		defer close(r.done)
		defer func() {
			if r.recovered = recover(); r.recovered != nil {
				p.add(leaseEvent{kind: "panic", id: c.id})
			}
		}()
		r.lease, r.err = p.j.lease(c.context(), c.delivery, c.session, c.request)
		p.add(leaseEvent{kind: "return", id: c.id, err: r.err})
	}()
	return r
}

func (r *leaseRun) returned() bool {
	select {
	case <-r.done:
		return true
	default:
		return false
	}
}

// awaitLease waits for r to return or panic. The bound only turns a deadlock into a failure.
func awaitLease(t *testing.T, r *leaseRun) {
	t.Helper()
	awaitClosed(t, r.done, "the return of lease "+strconv.Itoa(r.id))
}

// awaitAll waits for every run to return, and fails on any that panicked.
func awaitAll(t *testing.T, runs ...*leaseRun) {
	t.Helper()
	for _, r := range runs {
		awaitLease(t, r)
		require.Nilf(t, r.recovered, "lease %d panicked", r.id)
	}
}

// queueLeases starts one lease per call, in order, each only once the one before it has queued
// behind the batch in flight, so that leaseQ's FIFO order is exactly calls. A batch must be in
// flight. A correct lease can only queue behind it; one that returns instead is counted here, for
// the caller to assert on, rather than left to hang the test.
func queueLeases(t *testing.T, p *leaseProbe, calls ...leaseCall) []*leaseRun {
	t.Helper()
	q := &p.j.leaseQ
	out := make([]*leaseRun, len(calls))
	for k, c := range calls {
		out[k] = goLease(p, c)
		for {
			q.mu.Lock()
			queued := len(q.queue)
			q.mu.Unlock()
			returned := 0
			for _, r := range out[:k+1] {
				if r.returned() {
					returned++
				}
			}
			if queued+returned >= k+1 {
				break
			}
			runtime.Gosched()
		}
	}
	return out
}

func requireLeasesPending(t *testing.T, runs []*leaseRun, why string) {
	t.Helper()
	for _, r := range runs {
		require.Falsef(t, r.returned(), "lease %d returned %s", r.id, why)
	}
}

// holdFirstLeaseBatch gives j a probe whose first Sync is held, starts c on the idle journal so
// that c leads batch 1, and returns once that Sync has started: batch 1 is in flight, and every
// lease queued from now on waits behind it.
func holdFirstLeaseBatch(t *testing.T, p *leaseProbe, c leaseCall) *leaseRun {
	t.Helper()
	require.NotNil(t, p.syncGate, "the probe must hold its first Sync")
	lead := goLease(p, c)
	awaitClosed(t, p.syncGate.entered, "batch 1's Sync")
	return lead
}

// leaseCtx is a live context whose Err runs onErr first. commitLeases' first phase is the only
// code that asks a request's context for Err, so the hook runs exactly when that phase evaluates the
// request: it observes evaluation, and injects into it, with no seam in production code.
type leaseCtx struct {
	context.Context
	onErr func()
}

func (c leaseCtx) Err() error {
	c.onErr()
	return c.Context.Err()
}

// leaseToken is the k-th delivery nonce these tests mint: 64 lowercase hex characters, never all
// zeros, and never equal to a testDeliveryToken or to a leaseNearCap placeholder.
func leaseToken(k int) string { return fmt.Sprintf("a%063x", k) }

// leaseLine is the canonical journal line for l, exactly as a lease appends it.
func leaseLine(t *testing.T, l deliveryLease) string {
	t.Helper()
	b, err := json.Marshal(l)
	require.NoError(t, err)
	return string(b) + "\n"
}

// leaseLines is the journal content the leases of runs append, in order.
func leaseLines(t *testing.T, runs ...*leaseRun) string {
	t.Helper()
	var b strings.Builder
	for _, r := range runs {
		b.WriteString(leaseLine(t, r.lease))
	}
	return b.String()
}

// leasePositionOf is the seal of a journal holding exactly the complete lines of file.
func leasePositionOf(file []byte) deliveryPosition {
	chain, count := deliveryChainSeed, 0
	for rest := file; len(rest) > 0; count++ {
		end := bytes.IndexByte(rest, '\n') + 1
		if end == 0 {
			end = len(rest)
		}
		chain = deliveryChain(chain, rest[:end])
		rest = rest[end:]
	}
	return deliveryPosition{Version: core.EvidenceVersion, Bytes: int64(len(file)), Count: count, Chain: chain}
}

// admittedLeases is how many leases j has admitted, read under st.
func admittedLeases(j *deliveryJournal) int {
	j.st.Lock()
	defer j.st.Unlock()
	return len(j.leases)
}

// awaitClosing waits until a Release has begun to close j: it has taken Lock.mu and set closing.
// The bound only turns a deadlock into a failure.
func awaitClosing(t *testing.T, j *deliveryJournal) {
	t.Helper()
	deadline := time.Now().Add(ingestACKWait)
	for {
		j.st.Lock()
		closing := j.closing
		j.st.Unlock()
		if closing {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("Release never began to close the journal")
		}
		runtime.Gosched()
	}
}

// goRelease runs lock.Release on a new goroutine, logs its return on p, and delivers its result.
func goRelease(p *leaseProbe, lock *Lock) <-chan error {
	out := make(chan error, 1)
	go func() {
		err := lock.Release()
		p.add(leaseEvent{kind: "release", err: err})
		out <- err
	}()
	return out
}

func requireNotReleased(t *testing.T, released <-chan error, why string) {
	t.Helper()
	select {
	case err := <-released:
		t.Fatalf("Release returned %s: %v", why, err)
	default:
	}
}

// T9 — design §6.2, step 1. A batch appends in one Write, syncs once and seals once — one
// paths.WriteAtomic of the v1 position file — and assigns dense arrivals in queue order.
func TestDeliveryJournal_BatchCommitsOneWriteOneSyncOneSeal(t *testing.T) {
	_, _, journal := newTestDeliveryJournal(t)
	p := newLeaseProbe(journal)
	p.syncGate = newWALGate(t)
	const sess, queued = core.SessionID("t9"), 32
	lead := holdFirstLeaseBatch(t, p, leaseCall{delivery: leaseToken(0), session: sess, request: testDeliveryRequest("t9 lead")})
	calls := make([]leaseCall, queued)
	for k := range calls {
		calls[k] = leaseCall{id: k + 1, delivery: leaseToken(k + 1), session: sess, request: testDeliveryRequest("t9 " + strconv.Itoa(k))}
	}
	runs := queueLeases(t, p, calls...)
	requireLeasesPending(t, runs, "while batch 1 held its Sync")
	p.syncGate.release()
	awaitAll(t, append([]*leaseRun{lead}, runs...)...)
	require.NoError(t, lead.err)
	for _, r := range runs {
		require.NoErrorf(t, r.err, "lease %d", r.id)
	}

	// Dense arrivals, in queue order, each with the identity its arrival derives.
	require.Equal(t, uint64(1), lead.lease.ArrivalSeq)
	for k, r := range runs {
		want := testLeaseRecord(t, calls[k].delivery, sess, calls[k].request, uint64(k+2))
		require.Equalf(t, want, r.lease, "lease %d", r.id)
	}

	// One Write, one Sync and one seal per batch: batch 1 held the gate, batch 2 is the other 32.
	require.Equal(t, int32(2), p.writes.Load(), "one Write per batch")
	require.Equal(t, int32(2), p.syncs.Load(), "one Sync per batch")
	require.Equal(t, int32(2), p.seals.Load(), "one seal per batch")
	log := p.log()
	write, sync, seal, sealed := eventAt(t, log, "write", 2), eventAt(t, log, "sync", 2), eventAt(t, log, "seal", 2), eventAt(t, log, "sealed", 2)
	require.Equal(t, leaseLines(t, runs...), string(log[write].data), "batch 2's one Write carries its 32 lines, in queue order")
	require.Less(t, write, sync)
	require.Less(t, sync, seal)
	require.NoError(t, log[sealed].err)
	for _, r := range runs {
		require.Greaterf(t, eventAt(t, log, "return", r.id), sealed, "lease %d returned before its batch's seal", r.id)
	}

	// The seal names the whole file, and it is what the position sidecar now holds.
	file := readTestFile(t, journal.path)
	require.Equal(t, leaseLines(t, lead)+leaseLines(t, runs...), string(file))
	require.Equal(t, leasePositionOf(file), log[sealed].pos)
	position, err := journal.loadPosition()
	require.NoError(t, err)
	require.Equal(t, log[sealed].pos, position)
	requireIdle(t, &journal.leaseQ)
}

// T10 — design §6.2, step 1, invariant I3. While a batch's seal is in progress, and even once the
// seal is durable but its call has not returned, no member of the batch has returned, nothing of it
// is admitted, and Release cannot overtake the batch.
func TestDeliveryJournal_NoLeaseReleasedBeforeItsBatchSeal(t *testing.T) {
	root, lock, journal := newTestDeliveryJournal(t)
	p := newLeaseProbe(journal)
	p.syncGate = newWALGate(t)
	beforeSeal, afterSeal := newWALGate(t), newWALGate(t)
	p.onSeal = func(call int, seal func() error) error {
		if call != 2 {
			return seal()
		}
		beforeSeal.hold()
		err := seal()
		afterSeal.hold()
		return err
	}
	const sess = core.SessionID("t10")
	lead := holdFirstLeaseBatch(t, p, leaseCall{delivery: leaseToken(0), session: sess, request: testDeliveryRequest("t10")})
	calls := make([]leaseCall, 8)
	for k := range calls {
		calls[k] = leaseCall{id: k + 1, delivery: leaseToken(k + 1), session: sess, request: testDeliveryRequest("t10")}
	}
	runs := queueLeases(t, p, calls...)
	p.syncGate.release()
	awaitAll(t, lead)
	require.NoError(t, lead.err)
	awaitClosed(t, beforeSeal.entered, "batch 2's seal")

	// Batch 2's lines are written and synced, and the seal has not begun: nothing is sealed or
	// released.
	requireLeasesPending(t, runs, "before its batch's seal began")
	position, err := journal.loadPosition()
	require.NoError(t, err)
	require.Equal(t, 1, position.Count, "the position cannot advance before the batch's seal")
	info, err := os.Stat(paths.Long(journal.path))
	require.NoError(t, err)
	require.Greater(t, info.Size(), position.Bytes, "batch 2's lines are durable, unsealed and unreleased")
	require.Equal(t, 1, admittedLeases(journal))

	// Release waits for the batch in flight.
	released := goRelease(p, lock)
	awaitClosing(t, journal)
	requireNotReleased(t, released, "while a batch was sealing")
	require.FileExists(t, LockPath(root), "Release must not remove the lock under a batch in flight")

	// The seal is durable now, but its call has not returned: still nothing is admitted or released.
	beforeSeal.release()
	awaitClosed(t, afterSeal.entered, "the end of batch 2's seal")
	position, err = journal.loadPosition()
	require.NoError(t, err)
	require.Equal(t, 1+len(runs), position.Count)
	requireLeasesPending(t, runs, "before its batch's seal returned")
	require.Equal(t, 1, admittedLeases(journal), "admission follows the seal call")
	requireNotReleased(t, released, "while a batch's seal was still returning")

	afterSeal.release()
	awaitAll(t, runs...)
	for _, r := range runs {
		require.NoErrorf(t, r.err, "lease %d", r.id)
	}
	require.NoError(t, <-released)
	log := p.log()
	sealed := eventAt(t, log, "sealed", 2)
	for _, r := range runs {
		require.Greaterf(t, eventAt(t, log, "return", r.id), sealed, "lease %d returned before its batch's seal", r.id)
	}
	require.Greater(t, eventAt(t, log, "close", 0), sealed, "Release closed the journal under a batch in flight")
	require.Greater(t, eventAt(t, log, "release", 0), sealed, "Release returned under a batch in flight")
	position, err = journal.loadPosition()
	require.NoError(t, err)
	require.Equal(t, 1+len(runs), position.Count)
	_, err = journal.lease(context.Background(), leaseToken(99), sess, testDeliveryRequest("t10"))
	require.ErrorIs(t, err, core.ErrDegraded, "a released journal refuses")
}

// T11 — design §6.2, §2.7. A second copy of one delivery joins the batch that carries the first
// (J1, J3) or, queued behind it, is answered by the next batch as a known nonce only once that
// batch's checkFile has passed (J2). Either way it appends nothing and receives the identical lease;
// a copy with another binding gets ErrAppendOnly after a commit and the fault after a failure.
func TestDeliveryJournal_ConcurrentRedeliveryJoinsPendingBatch(t *testing.T) {
	req, other := testDeliveryRequest("t11"), testDeliveryRequest("t11 other")
	const sess = core.SessionID("t11")
	gate := leaseCall{delivery: leaseToken(0), session: "t11-gate", request: req}
	a := leaseToken(1)
	errFault := errors.New("private backend fixture")

	t.Run("J1: a copy later in the same batch joins the mint", func(t *testing.T) {
		_, _, journal := newTestDeliveryJournal(t)
		p := newLeaseProbe(journal)
		p.syncGate = newWALGate(t)
		lead := holdFirstLeaseBatch(t, p, gate)
		runs := queueLeases(t, p,
			leaseCall{id: 1, delivery: leaseToken(11), session: sess, request: req},
			leaseCall{id: 2, delivery: a, session: sess, request: req},
			leaseCall{id: 3, delivery: leaseToken(12), session: sess, request: req},
			leaseCall{id: 4, delivery: a, session: sess, request: req},
			leaseCall{id: 5, delivery: a, session: "t11-elsewhere", request: req},
			leaseCall{id: 6, delivery: a, session: sess, request: other},
		)
		p.syncGate.release()
		awaitAll(t, append([]*leaseRun{lead}, runs...)...)
		require.NoError(t, lead.err)
		for _, r := range runs[:4] {
			require.NoErrorf(t, r.err, "lease %d", r.id)
		}
		require.Equal(t, runs[1].lease, runs[3].lease, "the copy receives the very lease its batch minted")
		for _, r := range runs[4:] {
			require.ErrorIsf(t, r.err, core.ErrAppendOnly, "lease %d rebinds a nonce minted in its own batch", r.id)
			require.Equal(t, deliveryLease{}, r.lease)
		}
		require.Equal(t, []uint64{1, 2, 3}, []uint64{runs[0].lease.ArrivalSeq, runs[1].lease.ArrivalSeq, runs[2].lease.ArrivalSeq},
			"a copy takes no arrival")
		require.Equal(t, int32(2), p.writes.Load())
		require.Equal(t, leaseLines(t, lead, runs[0], runs[1], runs[2]), string(readTestFile(t, journal.path)),
			"the nonce has exactly one line")
	})

	t.Run("J3: a copy queued before its original's batch was cut joins it", func(t *testing.T) {
		_, _, journal := newTestDeliveryJournal(t)
		p := newLeaseProbe(journal)
		p.syncGate = newWALGate(t)
		lead := holdFirstLeaseBatch(t, p, gate)
		runs := queueLeases(t, p,
			leaseCall{id: 1, delivery: a, session: sess, request: req},
			leaseCall{id: 2, delivery: a, session: sess, request: req},
		)
		p.syncGate.release()
		awaitAll(t, append([]*leaseRun{lead}, runs...)...)
		require.NoError(t, runs[0].err)
		require.NoError(t, runs[1].err)
		require.Equal(t, runs[0].lease, runs[1].lease)
		require.Equal(t, int32(2), p.writes.Load())
		require.Equal(t, leaseLines(t, lead, runs[0]), string(readTestFile(t, journal.path)))
	})

	t.Run("J2: a copy queued behind its original's batch is answered as a known nonce", func(t *testing.T) {
		_, _, journal := newTestDeliveryJournal(t)
		p := newLeaseProbe(journal)
		p.syncGate = newWALGate(t)
		orig := holdFirstLeaseBatch(t, p, leaseCall{delivery: a, session: sess, request: req})
		runs := queueLeases(t, p,
			leaseCall{id: 1, delivery: a, session: sess, request: req},
			leaseCall{id: 2, delivery: a, session: sess, request: other},
		)
		requireLeasesPending(t, runs, "while the batch carrying the original held its Sync")
		p.syncGate.release()
		awaitAll(t, append([]*leaseRun{orig}, runs...)...)
		require.NoError(t, orig.err)
		require.NoError(t, runs[0].err)
		require.Equal(t, orig.lease, runs[0].lease)
		require.ErrorIs(t, runs[1].err, core.ErrAppendOnly)
		require.Equal(t, int32(1), p.writes.Load(), "a known nonce appends nothing")
		require.Equal(t, leaseLines(t, orig), string(readTestFile(t, journal.path)))
	})

	t.Run("J2: the next batch's checkFile gates the known-nonce answer", func(t *testing.T) {
		root, _, journal := newTestDeliveryJournal(t)
		positionPath := filepath.Join(paths.Of(root).State, deliveryPositionFile)
		p := newLeaseProbe(journal)
		p.syncGate = newWALGate(t)
		orig := holdFirstLeaseBatch(t, p, leaseCall{delivery: a, session: sess, request: req})
		var injected atomic.Bool
		ctx := leaseCtx{Context: context.Background(), onErr: func() {
			if !injected.CompareAndSwap(false, true) {
				return
			}
			// The original's batch has sealed and admitted by now; this is the next batch's
			// evaluation. Advance the sealed count behind the journal's back.
			if err := bumpTestPositionCount(positionPath); err != nil {
				t.Errorf("injecting the corruption: %v", err)
			}
		}}
		runs := queueLeases(t, p, leaseCall{id: 1, ctx: ctx, delivery: a, session: sess, request: req})
		p.syncGate.release()
		awaitAll(t, orig, runs[0])
		require.NoError(t, orig.err)
		require.True(t, injected.Load())
		require.ErrorIs(t, runs[0].err, core.ErrDegraded, "a known nonce is answered only once its batch's checkFile passed")
		require.Equal(t, deliveryLease{}, runs[0].lease)
		require.Error(t, journalFault(journal))
		require.Equal(t, int32(1), p.writes.Load())
	})

	t.Run("J2 behind a failed batch: the next batch refuses on the fault", func(t *testing.T) {
		_, _, journal := newTestDeliveryJournal(t)
		p := newLeaseProbe(journal)
		p.syncGate = newWALGate(t)
		p.onSync = func(call int) error {
			if call == 1 {
				return errFault
			}
			return journal.file.Sync()
		}
		orig := holdFirstLeaseBatch(t, p, leaseCall{delivery: a, session: sess, request: req})
		runs := queueLeases(t, p, leaseCall{id: 1, delivery: a, session: sess, request: req})
		p.syncGate.release()
		awaitAll(t, orig, runs[0])
		require.ErrorIs(t, orig.err, core.ErrDegraded)
		require.NotErrorIs(t, orig.err, errFault, "a backend error never reaches the caller")
		require.ErrorIs(t, runs[0].err, core.ErrDegraded)
		require.Equal(t, int32(1), p.writes.Load(), "the next batch never reaches the journal")
		require.Equal(t, int32(1), p.syncs.Load())
		require.Zero(t, admittedLeases(journal))
	})

	t.Run("a copy with another binding gets the fault when its batch fails", func(t *testing.T) {
		_, _, journal := newTestDeliveryJournal(t)
		p := newLeaseProbe(journal)
		p.syncGate = newWALGate(t)
		p.onSync = func(call int) error {
			if call == 2 {
				return errFault
			}
			return journal.file.Sync()
		}
		lead := holdFirstLeaseBatch(t, p, gate)
		runs := queueLeases(t, p,
			leaseCall{id: 1, delivery: a, session: sess, request: req},
			leaseCall{id: 2, delivery: a, session: sess, request: req},
			leaseCall{id: 3, delivery: a, session: sess, request: other},
		)
		p.syncGate.release()
		awaitAll(t, append([]*leaseRun{lead}, runs...)...)
		require.NoError(t, lead.err)
		for _, r := range runs {
			require.ErrorIsf(t, r.err, core.ErrDegraded, "lease %d depended on the failed append", r.id)
			require.NotErrorIsf(t, r.err, core.ErrAppendOnly, "lease %d", r.id)
			require.Equal(t, deliveryLease{}, r.lease)
		}
		require.Equal(t, 1, admittedLeases(journal))
	})
}

// bumpTestPositionCount rewrites the position sidecar at p, canonically, with its count advanced by
// one: a seal that no longer describes the journal.
func bumpTestPositionCount(p string) error {
	position, err := loadDeliveryPosition(p, deliveryChainSeed)
	if err != nil {
		return err
	}
	position.Count++
	encoded, err := json.Marshal(position)
	if err != nil {
		return err
	}
	return os.WriteFile(paths.Long(p), encoded, 0o600)
}

// t13Exhausted is a session whose arrival sequence T13 and T12 set to its maximum, so that its next
// lease is refused with ErrBudget.
const t13Exhausted = core.SessionID("exhausted")

// T13 — design §6.2, step 1. A short write, a sync failure and a seal failure in a batch of sixteen
// each poison the journal and fail every member whose answer depended on the append — the mints and
// the copies that joined them — while known nonces get their sealed lease and refusals keep their
// own error. Nothing is admitted, and a reopen recovers exactly as §3 rows 5 and 6 say.
func TestDeliveryJournal_BatchFailurePoisonsEveryDependentMember(t *testing.T) {
	for _, mode := range []string{"short write", "sync failure", "seal failure"} {
		t.Run(mode, func(t *testing.T) {
			root, lock, journal := newTestDeliveryJournal(t)
			ctx := context.Background()
			positionPath := filepath.Join(paths.Of(root).State, deliveryPositionFile)
			req := testDeliveryRequest("t13")
			other := testDeliveryRequest("t13 other")
			k1, err := journal.lease(ctx, leaseToken(901), "t13", req)
			require.NoError(t, err)
			k2, err := journal.lease(ctx, leaseToken(902), "t13", req)
			require.NoError(t, err)
			journal.st.Lock()
			journal.arrivals[t13Exhausted] = math.MaxUint64
			journal.st.Unlock()

			p := newLeaseProbe(journal)
			p.syncGate = newWALGate(t)
			switch mode {
			case "short write":
				p.onWrite = func(call int, b []byte) (int, error) {
					if call == 2 {
						return journal.file.Write(b[:len(b)/2])
					}
					return journal.file.Write(b)
				}
			case "sync failure":
				p.onSync = func(call int) error {
					if call == 2 {
						return errors.New("private backend fixture")
					}
					return journal.file.Sync()
				}
			case "seal failure":
				p.onSync = func(call int) error {
					err := journal.file.Sync()
					if call == 2 {
						if rerr := os.Remove(paths.Long(positionPath)); rerr != nil {
							t.Errorf("removing the position: %v", rerr)
						}
						if merr := os.Mkdir(paths.Long(positionPath), 0o700); merr != nil {
							t.Errorf("replacing the position with a directory: %v", merr)
						}
					}
					return err
				}
			}
			lead := holdFirstLeaseBatch(t, p, leaseCall{delivery: leaseToken(0), session: "t13-gate", request: req})
			canceled, cancel := context.WithCancel(ctx)
			cancel()
			oversize := core.SessionID(strings.Repeat("s", deliveryLeaseMaxLine))
			calls := []leaseCall{
				{id: 1, delivery: leaseToken(1), session: "s1", request: req},                // mint A
				{id: 2, delivery: leaseToken(1), session: "s1", request: req},                // joins A
				{id: 3, delivery: leaseToken(1), session: "s1", request: other},              // joins A, another binding
				{id: 4, delivery: k1.Delivery, session: "t13", request: req},                 // known
				{id: 5, delivery: k2.Delivery, session: "t13", request: other},               // known, another binding
				{id: 6, delivery: leaseToken(6), session: t13Exhausted, request: req},        // arrival overflow
				{id: 7, delivery: leaseToken(7), session: oversize, request: req},            // line bound
				{id: 8, delivery: "", session: "s1", request: req},                           // invalid nonce
				{id: 9, ctx: canceled, delivery: leaseToken(9), session: "s1", request: req}, // cancelled
				{id: 10, delivery: leaseToken(10), session: "s1", request: req},              // mint B
				{id: 11, delivery: leaseToken(11), session: "s2", request: req},              // mint C
				{id: 12, delivery: leaseToken(10), session: "s1", request: req},              // joins B
				{id: 13, delivery: k1.Delivery, session: "t13", request: req},                // known
				{id: 14, delivery: leaseToken(14), session: "s3", request: req},              // mint D
				{id: 15, delivery: leaseToken(15), session: "s1"},                            // zero request
				{id: 16, delivery: leaseToken(16), session: "s1", request: req},              // mint E
			}
			runs := queueLeases(t, p, calls...)
			p.syncGate.release()
			awaitAll(t, append([]*leaseRun{lead}, runs...)...)
			require.NoError(t, lead.err, "batch 1 commits; the fault is batch 2's")
			require.Equal(t, int32(2), p.writes.Load(), "batch 2 is the sixteen, cut together")

			dependent := map[int]bool{1: true, 2: true, 3: true, 10: true, 11: true, 12: true, 14: true, 16: true}
			for _, r := range runs {
				switch {
				case dependent[r.id]:
					require.ErrorIsf(t, r.err, core.ErrDegraded, "lease %d depended on the failed append", r.id)
					require.Equalf(t, deliveryLease{}, r.lease, "lease %d", r.id)
				case r.id == 4 || r.id == 13:
					require.NoErrorf(t, r.err, "lease %d is a known nonce, answered without the append", r.id)
					require.Equalf(t, k1, r.lease, "lease %d", r.id)
				case r.id == 5:
					require.ErrorIs(t, r.err, core.ErrAppendOnly)
				case r.id == 6 || r.id == 7:
					require.ErrorIsf(t, r.err, core.ErrBudget, "lease %d", r.id)
				case r.id == 8 || r.id == 15:
					require.ErrorIsf(t, r.err, core.ErrContract, "lease %d", r.id)
				case r.id == 9:
					require.ErrorIs(t, r.err, context.Canceled)
				}
			}

			// Nothing of batch 2 was admitted, and the handle is poisoned.
			journal.st.Lock()
			require.Len(t, journal.leases, 3)
			require.Equal(t, map[core.SessionID]uint64{"t13": 2, "t13-gate": 1, t13Exhausted: math.MaxUint64}, journal.arrivals)
			sealedPrefix := leaseLine(t, k1) + leaseLine(t, k2) + leaseLine(t, lead.lease)
			require.Equal(t, int64(len(sealedPrefix)), journal.bytes)
			require.Equal(t, leasePositionOf([]byte(sealedPrefix)).Chain, journal.chain)
			journal.st.Unlock()
			_, err = lock.openDeliveryJournal()
			require.Error(t, err, "a poisoned journal is not handed out")
			evidence := readTestFile(t, journal.path)
			_, err = journal.lease(ctx, leaseToken(99), "s1", req)
			require.ErrorIs(t, err, core.ErrDegraded)
			require.Equal(t, evidence, readTestFile(t, journal.path))
			require.NoError(t, lock.Release())

			if mode == "seal failure" {
				require.NoError(t, os.Remove(paths.Long(positionPath))) // an empty directory the test made
				sealed, err := json.Marshal(leasePositionOf([]byte(sealedPrefix)))
				require.NoError(t, err)
				require.NoError(t, os.WriteFile(paths.Long(positionPath), sealed, 0o600))
			}
			next, err := acquireTestDeliveryLock(root)
			require.NoError(t, err)
			t.Cleanup(func() { _ = next.Release() })
			recovered, err := next.openDeliveryJournal()
			if mode == "short write" {
				require.Error(t, err, "§3 row 5: a torn tail refuses the open")
				require.Nil(t, recovered)
				require.Equal(t, evidence, readTestFile(t, journal.path), "a torn tail is preserved, never consumed")
				return
			}
			// §3 rows 5 and 6: the batch's complete lines survived, unsealed and never released. The
			// open re-seals them, and each redelivery recovers the identity its failed batch assigned.
			require.NoError(t, err)
			position, err := recovered.loadPosition()
			require.NoError(t, err)
			require.Equal(t, 3+5, position.Count)
			for id, arrival := range map[int]uint64{1: 1, 10: 2, 16: 3, 11: 1, 14: 1} {
				c := calls[id-1]
				l, err := recovered.lease(ctx, c.delivery, c.session, c.request)
				require.NoErrorf(t, err, "lease %d", id)
				require.Equalf(t, testLeaseRecord(t, c.delivery, c.session, c.request, arrival), l, "lease %d", id)
			}
			position, err = recovered.loadPosition()
			require.NoError(t, err)
			require.Equal(t, 3+5, position.Count, "a recovered identity is a known nonce and appends nothing")
		})
	}
}

// leaseTestPanic is what a seam panics with in the panic test, naming where.
type leaseTestPanic struct{ at string }

// J-A2 on the lease pipeline (design §2.3, §2.5). A batch that panics poisons the journal and every
// member it had not resolved keeps the failure it started with, deliveryJournalError(), known
// nonces included. The panic itself continues on the leader's goroutine, as a panic inside lease
// always has; on the live path the IPC server's dispatch contains it and NAKs.
func TestDeliveryJournal_PanicInBatchPoisonsAndFailsEveryMember(t *testing.T) {
	for _, at := range []string{"sync", "seal"} {
		t.Run("a panic in the "+at, func(t *testing.T) {
			_, lock, journal := newTestDeliveryJournal(t)
			p := newLeaseProbe(journal)
			p.syncGate = newWALGate(t)
			switch at {
			case "sync":
				p.onSync = func(call int) error {
					if call == 2 {
						panic(leaseTestPanic{at: at})
					}
					return journal.file.Sync()
				}
			case "seal":
				p.onSeal = func(call int, seal func() error) error {
					if call == 2 {
						panic(leaseTestPanic{at: at})
					}
					return seal()
				}
			}
			req := testDeliveryRequest("panic")
			lead := holdFirstLeaseBatch(t, p, leaseCall{delivery: leaseToken(0), session: "p", request: req})
			runs := queueLeases(t, p,
				leaseCall{id: 1, delivery: leaseToken(1), session: "p", request: req},
				leaseCall{id: 2, delivery: leaseToken(2), session: "p", request: req},
				leaseCall{id: 3, delivery: leaseToken(1), session: "p", request: req},
				leaseCall{id: 4, delivery: leaseToken(0), session: "p", request: req},
				leaseCall{id: 5, delivery: leaseToken(5), session: "q", request: req},
			)
			p.syncGate.release()
			awaitAll(t, lead)
			require.NoError(t, lead.err)
			for _, r := range runs {
				awaitLease(t, r)
			}
			require.Equal(t, leaseTestPanic{at: at}, runs[0].recovered, "the panic continues on the leader's goroutine")
			for _, r := range runs[1:] {
				require.Nilf(t, r.recovered, "lease %d only followed the batch", r.id)
				require.ErrorIsf(t, r.err, core.ErrDegraded, "lease %d keeps the failure it started with", r.id)
				require.Equalf(t, deliveryLease{}, r.lease, "lease %d", r.id)
			}
			require.Equal(t, 1, admittedLeases(journal))
			require.Error(t, journalFault(journal), "a batch that panicked leaves the journal poisoned")
			_, err := lock.openDeliveryJournal()
			require.Error(t, err)
			requireIdle(t, &journal.leaseQ)
			require.NoError(t, lock.Release(), "the panicked batch left nothing in flight")
		})
	}
}

// t14Corruption is one way to make the journal's files disagree with its handle, as checkFile
// (step 1) detects it. inject runs with the journal's two paths.
type t14Corruption struct {
	name   string
	inject func(journal, position string) error
}

// t14Corruptions are the step-1 checkFile's detections. The journal cannot be removed or renamed
// over while its writer is open on Windows, whose handles carry no FILE_SHARE_DELETE, so its missing
// and replaced cases are exercised after a Release by TestDeliveryJournal_SealedPrefixCannotRewindOrChange;
// the position sidecar's same-content replacement is what every v1 seal already is, and only the
// step-2 held seal can see it.
func t14Corruptions() []t14Corruption {
	rewrite := func(change func(*deliveryPosition) []byte) func(string, string) error {
		return func(_, position string) error {
			pos, err := loadDeliveryPosition(position, deliveryChainSeed)
			if err != nil {
				return err
			}
			return os.WriteFile(paths.Long(position), change(&pos), 0o600)
		}
	}
	canonical := func(pos *deliveryPosition) []byte {
		b, _ := json.Marshal(pos)
		return b
	}
	return []t14Corruption{
		{"position missing", func(_, position string) error { return os.Remove(paths.Long(position)) }},
		{"position malformed", func(_, position string) error {
			return os.WriteFile(paths.Long(position), []byte(`{"private fixture":`), 0o600)
		}},
		{"position future version", rewrite(func(p *deliveryPosition) []byte { p.Version++; return canonical(p) })},
		{"position noncanonical", rewrite(func(p *deliveryPosition) []byte { return append(canonical(p), ' ') })},
		{"position count", rewrite(func(p *deliveryPosition) []byte { p.Count++; return canonical(p) })},
		{"position bytes", rewrite(func(p *deliveryPosition) []byte { p.Bytes--; return canonical(p) })},
		{"position chain", rewrite(func(p *deliveryPosition) []byte {
			p.Chain = testDeliveryRequest("unrelated chain")
			return canonical(p)
		})},
		{"journal truncated", func(journal, _ string) error {
			info, err := os.Stat(paths.Long(journal))
			if err != nil {
				return err
			}
			return os.Truncate(paths.Long(journal), info.Size()-1)
		}},
		{"journal extended", func(journal, _ string) error {
			f, err := os.OpenFile(paths.Long(journal), os.O_WRONLY|os.O_APPEND, 0o600)
			if err != nil {
				return err
			}
			_, werr := f.Write([]byte("{}\n"))
			return errors.Join(werr, f.Close())
		}},
	}
}

// t14Files is what a corruption left on disk, read the moment it was injected.
type t14Files struct {
	journal, position []byte
	positionMissing   bool
}

func readT14Files(journal, position string) (t14Files, error) {
	var f t14Files
	var err error
	if f.journal, err = os.ReadFile(paths.Long(journal)); err != nil {
		return f, err
	}
	f.position, err = os.ReadFile(paths.Long(position))
	if os.IsNotExist(err) {
		return t14Files{journal: f.journal, positionMissing: true}, nil
	}
	return f, err
}

// T14 — design §6.2, fix J-B4, step 1. A batch evaluates every member first, then runs its one
// checkFile, then appends: so a corruption made while the batch is still evaluating is detected
// before the Write, exactly as one made between batches is. Every step-1 corruption mode is
// injected at both points; each is detected before anything is written, every validated member —
// known nonces included — gets the fault, and the files are left exactly as the corruption left
// them. A committed batch's log shows evaluation, then Write, Sync, seal, then every return.
func TestDeliveryJournal_CheckRunsAfterEvaluationAndImmediatelyBeforeAppend(t *testing.T) {
	const members = 8
	req := testDeliveryRequest("t14")

	t.Run("the order of a committed batch", func(t *testing.T) {
		_, _, journal := newTestDeliveryJournal(t)
		p := newLeaseProbe(journal)
		p.syncGate = newWALGate(t)
		lead := holdFirstLeaseBatch(t, p, leaseCall{delivery: leaseToken(0), session: "t14", request: req})
		calls := make([]leaseCall, members)
		for k := range calls {
			id := k + 1
			calls[k] = leaseCall{
				id: id, delivery: leaseToken(id), session: "t14", request: req,
				ctx: leaseCtx{Context: context.Background(), onErr: func() { p.add(leaseEvent{kind: "eval", id: id}) }},
			}
		}
		runs := queueLeases(t, p, calls...)
		p.syncGate.release()
		awaitAll(t, append([]*leaseRun{lead}, runs...)...)
		log := p.log()
		write := eventAt(t, log, "write", 2)
		for _, r := range runs {
			require.NoErrorf(t, r.err, "lease %d", r.id)
			for _, e := range log {
				if e.kind == "eval" && e.id == r.id {
					require.Lessf(t, e.at, write, "lease %d was evaluated after its batch's Write", r.id)
				}
			}
		}
		sync, seal, sealed := eventAt(t, log, "sync", 2), eventAt(t, log, "seal", 2), eventAt(t, log, "sealed", 2)
		require.Less(t, write, sync)
		require.Less(t, sync, seal)
		require.Less(t, seal, sealed)
		for _, r := range runs {
			require.Greaterf(t, eventAt(t, log, "return", r.id), sealed, "lease %d", r.id)
		}
	})

	for _, c := range t14Corruptions() {
		for _, at := range []string{"between batches", "during evaluation"} {
			t.Run(c.name+" "+at, func(t *testing.T) {
				root, lock, journal := newTestDeliveryJournal(t)
				positionPath := filepath.Join(paths.Of(root).State, deliveryPositionFile)
				known, err := journal.lease(context.Background(), leaseToken(900), "t14", req)
				require.NoError(t, err)

				p := newLeaseProbe(journal)
				p.syncGate = newWALGate(t)
				var once sync.Once
				var left t14Files
				inject := func() {
					once.Do(func() {
						if err := c.inject(journal.path, positionPath); err != nil {
							t.Errorf("injecting %s: %v", c.name, err)
						}
						var err error
						if left, err = readT14Files(journal.path, positionPath); err != nil {
							t.Errorf("reading what %s left: %v", c.name, err)
						}
					})
				}
				if at == "between batches" {
					// After batch 1's seal is durable and before batch 2 starts.
					p.onSeal = func(call int, seal func() error) error {
						err := seal()
						if call == 1 {
							inject()
						}
						return err
					}
				}
				lead := holdFirstLeaseBatch(t, p, leaseCall{delivery: leaseToken(0), session: "t14", request: req})
				calls := make([]leaseCall, members)
				for k := range calls {
					calls[k] = leaseCall{id: k + 1, delivery: leaseToken(k + 1), session: "t14", request: req}
				}
				calls[2] = leaseCall{id: 3, delivery: known.Delivery, session: "t14", request: req}
				if at == "during evaluation" {
					// The batch's last member: every other member has been evaluated by now.
					calls[members-1].ctx = leaseCtx{Context: context.Background(), onErr: inject}
				}
				runs := queueLeases(t, p, calls...)
				p.syncGate.release()
				awaitAll(t, append([]*leaseRun{lead}, runs...)...)
				require.NoError(t, lead.err, "batch 1 was sealed before the corruption")
				for _, r := range runs {
					require.ErrorIsf(t, r.err, core.ErrDegraded, "lease %d: %s went undetected", r.id, c.name)
					require.Equalf(t, deliveryLease{}, r.lease, "lease %d", r.id)
				}
				require.Equal(t, int32(1), p.writes.Load(), "nothing was written after the corruption")
				require.Error(t, journalFault(journal))
				now, err := readT14Files(journal.path, positionPath)
				require.NoError(t, err)
				require.Equal(t, left, now, "the files are exactly as the corruption left them")
				_, err = lock.openDeliveryJournal()
				require.Error(t, err)
			})
		}
	}
}

// T15 — design §6.2, lease side, §3 row 17. Release waits for the batch in flight, which commits;
// the requests queued behind it fail at enter with deliveryJournalError; nothing is appended after
// Release returns.
func TestDeliveryJournal_ReleaseWaitsForInFlightBatchesAndFailsQueued(t *testing.T) {
	root, lock, journal := newTestDeliveryJournal(t)
	p := newLeaseProbe(journal)
	p.syncGate = newWALGate(t)
	req := testDeliveryRequest("t15")
	lead := holdFirstLeaseBatch(t, p, leaseCall{delivery: leaseToken(0), session: "t15", request: req})
	calls := make([]leaseCall, 8)
	for k := range calls {
		calls[k] = leaseCall{id: k + 1, delivery: leaseToken(k + 1), session: "t15", request: req}
	}
	calls[3] = leaseCall{id: 4, delivery: leaseToken(0), session: "t15", request: req} // a copy of the batch in flight
	runs := queueLeases(t, p, calls...)
	released := goRelease(p, lock)
	awaitClosing(t, journal)
	requireNotReleased(t, released, "while a batch was in flight")

	p.syncGate.release()
	awaitAll(t, append([]*leaseRun{lead}, runs...)...)
	require.NoError(t, lead.err, "the batch in flight commits")
	for _, r := range runs {
		require.ErrorIsf(t, r.err, core.ErrDegraded, "lease %d was queued behind a Release", r.id)
		require.Equalf(t, deliveryLease{}, r.lease, "lease %d", r.id)
	}
	require.NoError(t, <-released)
	log := p.log()
	sealed := eventAt(t, log, "sealed", 1)
	require.Greater(t, eventAt(t, log, "close", 0), sealed)
	require.Greater(t, eventAt(t, log, "release", 0), sealed)
	require.Equal(t, int32(1), p.writes.Load())
	require.Equal(t, int32(1), p.seals.Load())
	file := readTestFile(t, journal.path)
	require.Equal(t, leaseLines(t, lead), string(file), "only the batch in flight was appended")
	position, err := journal.loadPosition()
	require.NoError(t, err)
	require.Equal(t, leasePositionOf(file), position)
	require.NoFileExists(t, LockPath(root))

	_, err = journal.lease(context.Background(), leaseToken(99), "t15", req)
	require.ErrorIs(t, err, core.ErrDegraded)
	require.Equal(t, file, readTestFile(t, journal.path), "nothing is appended after Release returns")
	_, err = lock.openDeliveryJournal()
	require.Error(t, err)
}

// leaseOutcome is what one lease call answered.
type leaseOutcome struct {
	lease deliveryLease
	err   error
}

// leaseSentinels are the errors a lease answers with, compared by errors.Is.
var leaseSentinels = []error{context.Canceled, core.ErrContract, core.ErrBudget, core.ErrAppendOnly, core.ErrDegraded}

func requireSameLeaseOutcome(t *testing.T, id int, got, want leaseOutcome) {
	t.Helper()
	require.Equalf(t, want.err == nil, got.err == nil, "lease %d: batched %v, sequential %v", id, got.err, want.err)
	for _, s := range leaseSentinels {
		require.Equalf(t, errors.Is(want.err, s), errors.Is(got.err, s),
			"lease %d: errors.Is(%v) differs: batched %v, sequential %v", id, s, got.err, want.err)
	}
	require.Equalf(t, want.lease, got.lease, "lease %d", id)
}

// leaseStart is the state T12's two journals start from.
type leaseStart struct {
	entries int   // placeholder leases (leaseNearCap); 0 for an empty journal
	size    int64 // the journal's starting length, with entries > 0
	big     bool  // the script includes lines long enough to cross the bytes cap on their own
}

// leaseNearCap puts j, freshly opened and still empty, into the state it would hold after entries
// leases totalling size bytes, as far as a lease batch and checkFile can see: the file is size bytes
// long, j holds entries placeholder leases under nonces no request in these tests uses, and the
// position sidecar seals exactly that. The file's content is zeros, which nothing reads: T12 never
// reopens these journals. It is how T12 reaches the real deliveryLeaseMaxEntries and
// deliveryLeaseMaxBytes in the middle of a batch without first committing 65,536 leases or 64 MiB
// of them (compare sp20d4WriteCompletedHistory, which T12 would have to reopen 20 times per run).
func leaseNearCap(t *testing.T, j *deliveryJournal, entries int, size int64) {
	t.Helper()
	require.Zero(t, j.bytes, "leaseNearCap starts from an empty journal")
	require.NoError(t, os.Truncate(paths.Long(j.path), size))
	chain := core.HashBytes("qompack.delivery.lease.test.near-cap", nil)
	j.st.Lock()
	for i := range entries {
		d := fmt.Sprintf("d%063x", i+1)
		j.leases[d] = deliveryLease{Delivery: d}
	}
	j.bytes, j.chain = size, chain
	j.st.Unlock()
	encoded, err := json.Marshal(deliveryPosition{Version: core.EvidenceVersion, Bytes: size, Count: entries, Chain: chain})
	require.NoError(t, err)
	require.NoError(t, paths.WriteAtomic(filepath.Join(filepath.Dir(j.path), deliveryPositionFile), encoded, 0o600))
}

// newLeaseScript draws n lease requests from rng: fresh nonces over a few sessions and request
// hashes (the empty session among them), duplicates and rebindings of earlier nonces, every
// contract refusal, cancelled contexts, the exhausted session, at most one line over
// deliveryLeaseMaxLine and, with big, lines long enough to cross the bytes cap on their own.
func newLeaseScript(rng *rand.Rand, n int, big bool) []leaseCall {
	sessions := []core.SessionID{"t12-a", "t12-b", "t12-c", ""}
	requests := []core.Hash{testDeliveryRequest("t12 r0"), testDeliveryRequest("t12 r1"), testDeliveryRequest("t12 r2")}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	invalid := []leaseCall{
		{delivery: "", session: "t12-a", request: requests[0]},
		{delivery: strings.Repeat("a", 63), session: "t12-a", request: requests[0]},
		{delivery: strings.Repeat("A", 64), session: "t12-a", request: requests[0]},
		{delivery: strings.Repeat("0", 64), session: "t12-a", request: requests[0]},
		{delivery: leaseToken(7777), session: "t12-a"},
		{delivery: leaseToken(7778), session: core.SessionID([]byte{0xff}), request: requests[0]},
	}
	var seen []leaseCall // the first appearance of every well-formed nonce, bar the oversize one
	oversize := false
	calls := make([]leaseCall, 0, n)
	for len(calls) < n {
		id := len(calls) + 1
		c := leaseCall{delivery: leaseToken(id), session: sessions[rng.IntN(len(sessions))], request: requests[rng.IntN(len(requests))]}
		fresh := true
		switch roll := rng.IntN(100); {
		case roll < 40:
		case roll < 52 && len(seen) > 0:
			c, fresh = seen[rng.IntN(len(seen))], false
		case roll < 60 && len(seen) > 0:
			c, fresh = seen[rng.IntN(len(seen))], false
			if rng.IntN(2) == 0 {
				c.session += "-moved"
			} else {
				c.request = testDeliveryRequest("t12 rebound")
			}
		case roll < 68:
			c, fresh = invalid[rng.IntN(len(invalid))], false
		case roll < 76:
			c.ctx = canceled
		case roll < 81:
			c.session = t13Exhausted
		case roll < 84 && !oversize:
			oversize, fresh = true, false
			c.session = core.SessionID(strings.Repeat("o", deliveryLeaseMaxLine))
		case roll < 94 && big:
			c.session = core.SessionID(strings.Repeat("g", 600+rng.IntN(400)))
		}
		c.id = id
		if fresh {
			seen = append(seen, leaseCall{delivery: c.delivery, session: c.session, request: c.request})
		}
		calls = append(calls, c)
	}
	return calls
}

// T12 — design §6.2, a seeded property test. A batch answers every request exactly as the same
// requests made one lease call at a time, in queue order, would have been answered — the same
// errors.Is results and the same leases — and it leaves the journal the same bytes and the same
// final seal. The scripts mix fresh nonces, duplicates, rebindings, contract refusals, cancelled
// contexts, an exhausted arrival sequence and an oversize line, and two kinds of start reach the
// real entries cap and the real bytes cap in the middle of the batch.
func TestDeliveryJournal_BatchMatchesSequentialOutcomes(t *testing.T) {
	for seed := range uint64(8) {
		t.Run(fmt.Sprintf("seed %d", seed), func(t *testing.T) {
			runLeaseTwin(t, seed, leaseStart{}, 24)
		})
	}
	for seed := range uint64(2) {
		t.Run(fmt.Sprintf("entries cap, seed %d", seed), func(t *testing.T) {
			runLeaseTwin(t, 100+seed, leaseStart{entries: deliveryLeaseMaxEntries - 5, size: 4 << 10}, 16)
		})
	}
	t.Run("bytes cap", func(t *testing.T) {
		runLeaseTwin(t, 200, leaseStart{entries: 1, size: deliveryLeaseMaxBytes - 1400, big: true}, 16)
	})
}

func runLeaseTwin(t *testing.T, seed uint64, start leaseStart, n int) {
	script := newLeaseScript(rand.New(rand.NewPCG(seed, uint64(n))), n, start.big)
	gate := leaseCall{delivery: leaseToken(0), session: "t12-gate", request: testDeliveryRequest("t12 gate")}
	open := func() *deliveryJournal {
		_, _, j := newTestDeliveryJournal(t)
		if start.entries > 0 {
			leaseNearCap(t, j, start.entries, start.size)
		}
		j.st.Lock()
		j.arrivals[t13Exhausted] = math.MaxUint64
		j.st.Unlock()
		return j
	}

	// The twin: the gate, then every request alone, in queue order, each a batch of one.
	twin := open()
	twinProbe := newLeaseProbe(twin)
	want := make([]leaseOutcome, len(script))
	_, err := twin.lease(context.Background(), gate.delivery, gate.session, gate.request)
	require.NoError(t, err)
	for k, c := range script {
		l, err := twin.lease(c.context(), c.delivery, c.session, c.request)
		want[k] = leaseOutcome{lease: l, err: err}
	}

	// The batch: the same requests queued behind the gate's batch, so that they are cut together.
	j := open()
	p := newLeaseProbe(j)
	p.syncGate = newWALGate(t)
	lead := holdFirstLeaseBatch(t, p, gate)
	runs := queueLeases(t, p, script...)
	p.syncGate.release()
	awaitAll(t, append([]*leaseRun{lead}, runs...)...)
	require.NoError(t, lead.err)
	for k, r := range runs {
		requireSameLeaseOutcome(t, r.id, leaseOutcome{lease: r.lease, err: r.err}, want[k])
	}

	got, wantFile := readTestFile(t, j.path), readTestFile(t, twin.path)
	require.Len(t, got, len(wantFile))
	require.Equal(t, string(wantFile[start.size:]), string(got[start.size:]),
		"the batch appends exactly the bytes one call at a time appends")
	gotSeal, err := j.loadPosition()
	require.NoError(t, err)
	wantSeal, err := twin.loadPosition()
	require.NoError(t, err)
	require.Equal(t, wantSeal, gotSeal)
	require.Equal(t, int64(len(got)), gotSeal.Bytes)
	for _, c := range script {
		gotLease, gotOK := j.leases[c.delivery]
		wantLease, wantOK := twin.leases[c.delivery]
		require.Equal(t, wantOK, gotOK, c.delivery)
		require.Equal(t, wantLease, gotLease, c.delivery)
	}
	if twinProbe.writes.Load() > 2 {
		require.Less(t, p.writes.Load(), twinProbe.writes.Load(), "the queued requests were not batched")
	}
	requireIdle(t, &j.leaseQ)
}

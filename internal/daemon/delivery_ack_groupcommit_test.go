package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math/rand/v2"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/paths"
)

// The acknowledgement group-commit tests: design §6.2 T17, T18 and T19, and the acknowledgement
// side of T9, T10, T13 and T14 (T15 and T16 cover both pipelines, in
// delivery_lease_groupcommit_test.go). Like the lease side, they are about the batch and not about
// the format: sealAck is one call per batch whichever seal this build writes, saveAckPosition's
// paths.WriteAtomic of the v1 sidecar or the held slot write.
// They run the production acknowledge on goroutines of their own, the way the ingest workers and the
// drainer call it, and watch the acknowledgement journal through its two seams, ackWriter and
// sealAck, with the lease tests' probe. Every ordering they rely on is made by holding a Sync or a
// seal open and by ackQ's own length. The clock only bounds waits that a correct build never
// reaches, so that a deadlock fails the test instead of hanging it.

// newAckProbe is newLeaseProbe for the acknowledgement journal: it instruments j's ackWriter and
// sealAck in place of its writer and sealLease.
func newAckProbe(j *deliveryJournal) *leaseProbe {
	p := &leaseProbe{j: j}
	j.ackWriter = p.writerFor(j.ackFile)
	j.sealAck = p.sealWith(j.sealAck)
	return p
}

// ackCall is one acknowledge request. A nil ctx is context.Background().
type ackCall struct {
	id       int
	ctx      context.Context
	delivery string
	obs      core.ObservationID
	root     core.Hash
}

func (c ackCall) context() context.Context {
	if c.ctx == nil {
		return context.Background()
	}
	return c.ctx
}

// ackOf is request id, acknowledging l with root.
func ackOf(id int, l deliveryLease, root core.Hash) ackCall {
	return ackCall{id: id, delivery: l.Delivery, obs: l.ObservationID, root: root}
}

// ackRun is one acknowledge running on its own goroutine. err and recovered are final once done
// is closed.
type ackRun struct {
	ackCall
	done      chan struct{}
	err       error
	recovered any
}

// goAck runs c on a new goroutine and logs its return, or its panic, on p the moment it happens.
func goAck(p *leaseProbe, c ackCall) *ackRun {
	r := &ackRun{ackCall: c, done: make(chan struct{})}
	go func() {
		defer close(r.done)
		defer func() {
			if r.recovered = recover(); r.recovered != nil {
				p.add(leaseEvent{kind: "panic", id: c.id})
			}
		}()
		r.err = p.j.acknowledge(c.context(), c.delivery, c.obs, c.root)
		p.add(leaseEvent{kind: "return", id: c.id, err: r.err})
	}()
	return r
}

func (r *ackRun) returned() bool {
	select {
	case <-r.done:
		return true
	default:
		return false
	}
}

// awaitAck waits for r to return or panic. The bound only turns a deadlock into a failure.
func awaitAck(t *testing.T, r *ackRun) {
	t.Helper()
	awaitClosed(t, r.done, "the return of acknowledgement "+strconv.Itoa(r.id))
}

// awaitAcks waits for every run to return, and fails on any that panicked.
func awaitAcks(t *testing.T, runs ...*ackRun) {
	t.Helper()
	for _, r := range runs {
		awaitAck(t, r)
		require.Nilf(t, r.recovered, "acknowledgement %d panicked", r.id)
	}
}

// queueAcks is queueLeases for ackQ: it starts one acknowledge per call, in order, each only once
// the one before it has queued behind the batch in flight, so that ackQ's FIFO order is exactly
// calls. A batch must be in flight. One that returns instead of queueing is counted here, for the
// caller to assert on; one that does neither fails the test.
func queueAcks(t *testing.T, p *leaseProbe, calls ...ackCall) []*ackRun {
	t.Helper()
	q := &p.j.ackQ
	out := make([]*ackRun, len(calls))
	for k, c := range calls {
		out[k] = goAck(p, c)
		deadline := time.Now().Add(ingestACKWait)
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
			if time.Now().After(deadline) {
				t.Fatalf("acknowledgement %d neither queued behind the batch in flight nor returned", c.id)
			}
			runtime.Gosched()
		}
	}
	return out
}

func requireAcksPending(t *testing.T, runs []*ackRun, why string) {
	t.Helper()
	for _, r := range runs {
		require.Falsef(t, r.returned(), "acknowledgement %d returned %s", r.id, why)
	}
}

// holdFirstAckBatch starts c on an idle ackQ, so that c leads batch 1, and returns once that
// batch's Sync, which p holds, has started: every acknowledgement queued from now on waits behind it.
func holdFirstAckBatch(t *testing.T, p *leaseProbe, c ackCall) *ackRun {
	t.Helper()
	require.NotNil(t, p.syncGate, "the probe must hold its first Sync")
	lead := goAck(p, c)
	awaitClosed(t, p.syncGate.entered, "batch 1's Sync")
	return lead
}

// leaseEach leases leaseToken(k), for k from 0 to n-1, each under a session of its own, and
// returns the leases by k. The leases run concurrently, so they batch; a session of its own gives
// every delivery arrival 1, so each lease is the same whichever batch it lands in, and two journals
// given the same n hold the same leases.
func leaseEach(t *testing.T, j *deliveryJournal, n int) []deliveryLease {
	t.Helper()
	out, errs := make([]deliveryLease, n), make([]error, n)
	var wg sync.WaitGroup
	for k := range n {
		wg.Go(func() {
			session := core.SessionID("ack-" + strconv.Itoa(k))
			out[k], errs[k] = j.lease(context.Background(), leaseToken(k), session, testDeliveryRequest("ack"))
		})
	}
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	awaitClosed(t, done, "the fixture's leases")
	for k, err := range errs {
		require.NoErrorf(t, err, "lease %d", k)
	}
	return out
}

// ackLines is the journal content the acknowledgements of calls append, in order.
func ackLines(t *testing.T, calls ...ackCall) string {
	t.Helper()
	var b strings.Builder
	for _, c := range calls {
		line, err := json.Marshal(deliveryAck{Version: core.EvidenceVersion, Delivery: c.delivery, ObservationID: c.obs, Root: c.root})
		require.NoError(t, err)
		b.Write(line)
		b.WriteByte('\n')
	}
	return b.String()
}

// ackPositionOf is the seal of an acknowledgement journal holding exactly the complete lines of
// file.
func ackPositionOf(file []byte) deliveryPosition { return positionOf(deliveryAckChainSeed, file) }

func ackPositionPath(j *deliveryJournal) string {
	return filepath.Join(filepath.Dir(j.path), deliveryAckPositionFile)
}

// loadAckPosition reads j's acknowledgement position sidecar, in whichever format it holds. It
// asks only what the sidecar SEALS, which is a fact about the journal and not about the format, so
// it goes through the dual reader the production loadPosition uses (design §5) rather than the
// strict v1 one: a format-2 build's sidecar is a 32 KiB A/B image that the v1 reader refuses by
// design, and refusing it here would make these tests say "the seal is unreadable" where they mean
// to say "the seal has not advanced".
func loadAckPosition(t *testing.T, j *deliveryJournal) deliveryPosition {
	t.Helper()
	position, _, _, err := loadDeliverySeal(ackPositionPath(j), deliveryAckChainSeed, deliveryAckChainDomain)
	require.NoError(t, err)
	return position
}

// admittedAcks is how many acknowledgements j has admitted, read under st.
func admittedAcks(j *deliveryJournal) int {
	j.st.Lock()
	defer j.st.Unlock()
	return len(j.acks)
}

// ackNearCap is leaseNearCap for the acknowledgement journal: it puts j, whose acknowledgement
// journal is still empty, into the state that journal would hold after entries acknowledgements
// totalling size bytes, as far as an acknowledgement batch and checkAckFile can see. The file is
// size zero bytes, which nothing reads, since these journals are never reopened; j holds entries
// placeholder acknowledgements under nonces no request in these tests uses; and the position
// sidecar seals exactly that.
func ackNearCap(t *testing.T, j *deliveryJournal, entries int, size int64) {
	t.Helper()
	require.Zero(t, j.ackBytes, "ackNearCap starts from an empty acknowledgement journal")
	require.NoError(t, os.Truncate(paths.Long(j.ackPath), size))
	chain := core.HashBytes("qompack.delivery.ack.test.near-cap", nil)
	j.st.Lock()
	for i := range entries {
		d := fmt.Sprintf("d%063x", i+1)
		j.acks[d] = deliveryAck{Delivery: d}
	}
	j.ackBytes, j.ackChain = size, chain
	j.st.Unlock()
	// Sealed through the journal's own seal, so the fixture seals the way this build's batches do:
	// the v1 sidecar's paths.WriteAtomic at format 1, the held slot's WriteAt at format 2. Writing
	// v1 bytes by hand would land a foreign file over a held seal, which is a corruption the next
	// check is right to refuse and has nothing to do with being near the cap.
	require.NoError(t, j.sealAck(size, entries, chain))
}

// T9 and T10 on the acknowledgement side — design §6.2, §2.8. An acknowledgement batch
// appends in one Write, syncs once and seals once (one call through sealAck),
// with every member evaluated before that Write. While its seal is in progress, and even once the
// seal is durable but its call has not returned, no member has returned, and nothing of the batch
// is admitted or on the frontier.
func TestDeliveryJournal_AckBatchCommitsOneWriteOneSyncOneSeal(t *testing.T) {
	_, _, journal := newTestDeliveryJournal(t)
	const queued = 32
	leases := leaseEach(t, journal, queued+1)
	root := testDeliveryRequest("t9 acknowledgement root")
	p := newAckProbe(journal)
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
	lead := holdFirstAckBatch(t, p, ackOf(0, leases[0], root))
	calls := make([]ackCall, queued)
	for k := range calls {
		id := k + 1
		calls[k] = ackOf(id, leases[id], root)
		calls[k].ctx = leaseCtx{Context: context.Background(), onErr: func() { p.add(leaseEvent{kind: "eval", id: id}) }}
	}
	runs := queueAcks(t, p, calls...)
	requireAcksPending(t, runs, "while batch 1 held its Sync")
	p.syncGate.release()
	awaitAcks(t, lead)
	require.NoError(t, lead.err)
	awaitClosed(t, beforeSeal.entered, "batch 2's seal")

	// Batch 2's lines are written and synced, and its seal has not begun: nothing is sealed,
	// admitted or answered.
	requireAcksPending(t, runs, "before its batch's seal began")
	position := loadAckPosition(t, journal)
	require.Equal(t, 1, position.Count, "the position cannot advance before the batch's seal")
	info, err := os.Stat(paths.Long(journal.ackPath))
	require.NoError(t, err)
	require.Greater(t, info.Size(), position.Bytes, "batch 2's lines are durable, unsealed and unreleased")
	require.Equal(t, 1, admittedAcks(journal))
	require.False(t, acknowledgedWithin(t, journal, leases[1].Delivery), "an unsealed acknowledgement is not on the frontier")

	// The seal is durable now, but its call has not returned: still nothing is admitted or answered.
	beforeSeal.release()
	awaitClosed(t, afterSeal.entered, "the end of batch 2's seal")
	require.Equal(t, 1+queued, loadAckPosition(t, journal).Count)
	requireAcksPending(t, runs, "before its batch's seal returned")
	require.Equal(t, 1, admittedAcks(journal), "admission follows the seal call")
	require.False(t, acknowledgedWithin(t, journal, leases[1].Delivery))

	afterSeal.release()
	awaitAcks(t, runs...)
	for _, r := range runs {
		require.NoErrorf(t, r.err, "acknowledgement %d", r.id)
		require.Truef(t, journal.acknowledged(r.delivery), "acknowledgement %d", r.id)
	}
	require.Equal(t, int32(2), p.writes.Load(), "one Write per batch")
	require.Equal(t, int32(2), p.syncs.Load(), "one Sync per batch")
	require.Equal(t, int32(2), p.seals.Load(), "one seal per batch")
	log := p.log()
	write, syncAt := eventAt(t, log, "write", 2), eventAt(t, log, "sync", 2)
	seal, sealed := eventAt(t, log, "seal", 2), eventAt(t, log, "sealed", 2)
	require.Equal(t, ackLines(t, calls...), string(log[write].data), "batch 2's one Write carries its 32 lines, in queue order")
	require.Less(t, write, syncAt)
	require.Less(t, syncAt, seal)
	require.NoError(t, log[sealed].err)
	for _, r := range runs {
		require.Lessf(t, eventAt(t, log, "eval", r.id), write, "acknowledgement %d was evaluated after its batch's Write", r.id)
		require.Greaterf(t, eventAt(t, log, "return", r.id), sealed, "acknowledgement %d returned before its batch's seal", r.id)
	}

	// The seal names the whole file, and it is what the position sidecar now holds.
	file := readTestFile(t, journal.ackPath)
	require.Equal(t, ackLines(t, lead.ackCall)+ackLines(t, calls...), string(file))
	require.Equal(t, ackPositionOf(file), log[sealed].pos)
	require.Equal(t, log[sealed].pos, loadAckPosition(t, journal))
	requireIdle(t, &journal.ackQ)
}

// ackTestPanic is what a seam panics with in the acknowledgement failure test, naming where.
type ackTestPanic struct{ at string }

// T13 and J-A2 on the acknowledgement side — design §6.2, §3 rows 12 and 13. A short
// write, a sync failure, a seal failure, and a panic in the Sync or in the seal, each in a batch of
// sixteen, poison the journal and fail every member whose answer depended on the append: the new
// acknowledgements and the copies that joined them. The rest keep the answer the first phase gave
// them, a panic included: an acknowledgement an earlier batch committed is answered nil, without a
// check, as it always was, and each refusal keeps its own error. A panic continues on the leader's
// goroutine. Nothing is admitted, the one fault refuses leases too, and a reopen recovers as §3 row
// 12 says: a torn tail refuses the open and is preserved; a complete one is re-sealed, and its
// acknowledgements are on the frontier.
func TestDeliveryJournal_AckBatchFailurePoisonsEveryDependentMember(t *testing.T) {
	for _, mode := range []string{"short write", "sync failure", "seal failure", "panic in the Sync", "panic in the seal"} {
		t.Run(mode, func(t *testing.T) {
			root, lock, journal := newTestDeliveryJournal(t)
			ctx := context.Background()
			positionPath := ackPositionPath(journal)
			errFault := errors.New("private backend fixture")
			// l[0] leads batch 1, l[1] and l[2] are acknowledged before it, and l[3] to l[7] are
			// batch 2's new acknowledgements.
			l := leaseEach(t, journal, 8)
			for _, k := range l[1:3] {
				require.NoError(t, journal.acknowledge(ctx, k.Delivery, k.ObservationID, core.Hash{}))
			}
			p := newAckProbe(journal)
			p.syncGate = newWALGate(t)
			switch mode {
			case "short write":
				p.onWrite = func(call int, b []byte) (int, error) {
					if call == 2 {
						return journal.ackFile.Write(b[:len(b)/2])
					}
					return journal.ackFile.Write(b)
				}
			case "sync failure":
				p.onSync = func(call int) error {
					if call == 2 {
						return errFault
					}
					return journal.ackFile.Sync()
				}
			case "seal failure":
				p.onSync = func(call int) error {
					err := journal.ackFile.Sync()
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
			case "panic in the Sync":
				p.onSync = func(call int) error {
					if call == 2 {
						panic(ackTestPanic{at: mode})
					}
					return journal.ackFile.Sync()
				}
			case "panic in the seal":
				p.onSeal = func(call int, seal func() error) error {
					if call == 2 {
						panic(ackTestPanic{at: mode})
					}
					return seal()
				}
			}
			lead := holdFirstAckBatch(t, p, ackOf(0, l[0], core.Hash{}))
			other := testDeliveryRequest("t13 other root")
			canceled, cancel := context.WithCancel(ctx)
			cancel()
			calls := []ackCall{
				ackOf(1, l[3], core.Hash{}),                                              // new A
				ackOf(2, l[3], core.Hash{}),                                              // joins A
				ackOf(3, l[3], other),                                                    // joins A, another root
				ackOf(4, l[1], core.Hash{}),                                              // committed
				{id: 5, delivery: leaseToken(900), obs: l[3].ObservationID},              // never leased
				{id: 6, delivery: l[4].Delivery, obs: l[5].ObservationID},                // another delivery's identity
				{id: 7, delivery: "", obs: l[4].ObservationID},                           // invalid nonce
				{id: 8, delivery: l[4].Delivery},                                         // no identity
				{id: 9, ctx: canceled, delivery: l[5].Delivery, obs: l[5].ObservationID}, // cancelled
				ackOf(10, l[4], core.Hash{}),                                             // new B
				ackOf(11, l[5], core.Hash{}),                                             // new C
				ackOf(12, l[4], core.Hash{}),                                             // joins B
				ackOf(13, l[1], other),                                                   // committed, another root
				ackOf(14, l[6], core.Hash{}),                                             // new D
				ackOf(15, l[2], core.Hash{}),                                             // committed
				ackOf(16, l[7], core.Hash{}),                                             // new E
			}
			runs := queueAcks(t, p, calls...)
			p.syncGate.release()
			awaitAcks(t, lead)
			require.NoError(t, lead.err, "batch 1 commits; the fault is batch 2's")
			for _, r := range runs {
				awaitAck(t, r)
			}
			require.Equal(t, int32(2), p.writes.Load(), "batch 2 is the sixteen, cut together")

			panicked := strings.HasPrefix(mode, "panic")
			dependent := map[int]bool{1: true, 2: true, 3: true, 10: true, 11: true, 12: true, 14: true, 16: true}
			for _, r := range runs {
				if panicked && r.id == 1 {
					require.Equal(t, ackTestPanic{at: mode}, r.recovered, "the panic continues on the leader's goroutine")
					continue
				}
				require.Nilf(t, r.recovered, "acknowledgement %d only followed the batch", r.id)
				switch {
				case dependent[r.id]:
					require.ErrorIsf(t, r.err, core.ErrDegraded, "acknowledgement %d depended on the failed append", r.id)
					require.NotErrorIsf(t, r.err, errFault, "acknowledgement %d: a backend error never reaches the caller", r.id)
				case r.id == 4 || r.id == 13 || r.id == 15:
					require.NoErrorf(t, r.err, "acknowledgement %d was committed by an earlier batch", r.id)
				case r.id == 9:
					require.ErrorIs(t, r.err, context.Canceled)
				default:
					require.ErrorIsf(t, r.err, core.ErrContract, "acknowledgement %d", r.id)
				}
			}

			// Nothing of batch 2 was admitted, and the handle is poisoned for both halves. The state
			// is copied out under st and asserted once st is released, so that a failing assertion
			// cannot leave st held and hang the cleanup's Release.
			journal.st.Lock()
			admitted := maps.Clone(journal.acks)
			size, chain := journal.ackBytes, journal.ackChain
			journal.st.Unlock()
			sealedPrefix := ackLines(t, ackOf(0, l[1], core.Hash{}), ackOf(0, l[2], core.Hash{}), lead.ackCall)
			require.Len(t, admitted, 3)
			require.Equal(t, int64(len(sealedPrefix)), size)
			require.Equal(t, ackPositionOf([]byte(sealedPrefix)).Chain, chain)
			require.Error(t, journalFault(journal))
			_, err := lock.openDeliveryJournal()
			require.Error(t, err, "a poisoned journal is not handed out")
			ackEvidence, leaseEvidence := readTestFile(t, journal.ackPath), readTestFile(t, journal.path)
			require.ErrorIs(t, journal.acknowledge(ctx, l[3].Delivery, l[3].ObservationID, core.Hash{}), core.ErrDegraded)
			_, err = journal.lease(ctx, leaseToken(99), "t13", testDeliveryRequest("t13"))
			require.ErrorIs(t, err, core.ErrDegraded, "the one fault refuses leases too")
			require.False(t, journal.acknowledged(l[1].Delivery), "a poisoned journal cannot say what is acknowledged")
			require.Equal(t, ackEvidence, readTestFile(t, journal.ackPath))
			require.Equal(t, leaseEvidence, readTestFile(t, journal.path))
			requireIdle(t, &journal.ackQ)
			require.NoError(t, lock.Release(), "the failed batch left nothing in flight")

			if mode == "seal failure" {
				require.NoError(t, os.Remove(paths.Long(positionPath))) // an empty directory the test made
				sealed, err := json.Marshal(ackPositionOf([]byte(sealedPrefix)))
				require.NoError(t, err)
				require.NoError(t, os.WriteFile(paths.Long(positionPath), sealed, 0o600))
			}
			next, err := acquireTestDeliveryLock(root)
			require.NoError(t, err)
			t.Cleanup(func() { _ = next.Release() })
			recovered, err := next.openDeliveryJournal()
			if mode == "short write" {
				require.Error(t, err, "§3 row 12: a torn acknowledgement tail refuses the open")
				require.Nil(t, recovered)
				require.Equal(t, ackEvidence, readTestFile(t, journal.ackPath), "a torn tail is preserved, never consumed")
				return
			}
			// §3 row 12: the batch's complete lines survived, unsealed and never answered. The open
			// re-seals them, and the deliveries they acknowledge are on the frontier.
			require.NoError(t, err)
			require.Equal(t, 3+5, loadAckPosition(t, recovered).Count)
			for _, k := range l[3:] {
				require.Truef(t, recovered.acknowledged(k.Delivery), "delivery %s", k.Delivery)
				require.NoError(t, recovered.acknowledge(ctx, k.Delivery, k.ObservationID, core.Hash{}), "a redelivery is idempotent")
			}
			require.Equal(t, 3+5, loadAckPosition(t, recovered).Count, "an idempotent acknowledgement appends nothing")
		})
	}
}

// T14 on the acknowledgement side — design §6.2, fix J-B4. An acknowledgement batch evaluates every
// member first, then runs its one checkAckFile, then appends: so a corruption of the acknowledgement
// files made while the batch is still evaluating is detected before the Write, exactly as one made
// between batches is. Each of T14's corruption modes, in each write format, applied to the
// acknowledgement journal and its position, is detected before anything is written; every new
// acknowledgement gets the fault, and the files are left exactly as the corruption left them. A
// member whose delivery an earlier batch acknowledged is answered nil without a check, as one
// acknowledge call always was.
//
// The modes are drawn against the ACKNOWLEDGEMENT journal's own seed and domain, which is what makes
// the v2 rewrites here rewrite this sidecar rather than one summed for the lease journal.
func TestDeliveryJournal_AckCheckRunsAfterEvaluationAndImmediatelyBeforeAppend(t *testing.T) {
	const members = 8
	for _, format := range []int{1, 2} {
		t.Run(formatName(format), func(t *testing.T) {
			runAckT14Corruptions(t, format, members)
		})
	}
}

// runAckT14Corruptions is the acknowledgement T14's corruption table for one write format.
func runAckT14Corruptions(t *testing.T, format, members int) {
	t.Helper()
	for _, c := range t14Corruptions(format, deliveryAckChainSeed, deliveryAckChainDomain) {
		for _, at := range []string{"between batches", "during evaluation"} {
			t.Run(c.name+" "+at, func(t *testing.T) {
				lock, journal := openSealFormat(t, t.TempDir(), format)
				ctx := context.Background()
				positionPath := ackPositionPath(journal)
				// l[0] leads batch 1, l[1] is acknowledged before it, and the rest are batch 2's.
				l := leaseEach(t, journal, members+2)
				require.NoError(t, journal.acknowledge(ctx, l[1].Delivery, l[1].ObservationID, core.Hash{}))

				p := newAckProbe(journal)
				p.syncGate = newWALGate(t)
				var once sync.Once
				var left t14Files
				inject := func() {
					once.Do(func() {
						if err := c.inject(journal.ackPath, positionPath); err != nil {
							t.Errorf("injecting %s: %v", c.name, err)
						}
						var err error
						if left, err = readT14Files(journal.ackPath, positionPath); err != nil {
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
				lead := holdFirstAckBatch(t, p, ackOf(0, l[0], core.Hash{}))
				calls := make([]ackCall, members)
				for k := range calls {
					calls[k] = ackOf(k+1, l[k+2], core.Hash{})
				}
				calls[2] = ackOf(3, l[1], core.Hash{}) // acknowledged before batch 1
				if at == "during evaluation" {
					// The batch's last member: every other member has been evaluated by now.
					calls[members-1].ctx = leaseCtx{Context: context.Background(), onErr: inject}
				}
				runs := queueAcks(t, p, calls...)
				p.syncGate.release()
				awaitAcks(t, append([]*ackRun{lead}, runs...)...)
				require.NoError(t, lead.err, "batch 1 was sealed before the corruption")
				for _, r := range runs {
					if r.id == 3 {
						require.NoError(t, r.err, "an acknowledged delivery is answered without a check")
						continue
					}
					require.ErrorIsf(t, r.err, core.ErrDegraded, "acknowledgement %d: %s went undetected", r.id, c.name)
				}
				require.Equal(t, int32(1), p.writes.Load(), "nothing was written after the corruption")
				require.Error(t, journalFault(journal))
				now, err := readT14Files(journal.ackPath, positionPath)
				require.NoError(t, err)
				require.Equal(t, left, now, "the files are exactly as the corruption left them")
				_, err = lock.openDeliveryJournal()
				require.Error(t, err)
			})
		}
	}
}

// Each member of an acknowledgement batch is answered in the order one acknowledge call always made
// its checks (design §2.8): ctx, then the gate, then validation and the lease match, then the
// idempotent answer for a delivery already acknowledged, which needs no checkAckFile, then
// checkAckFile, and only then the entries bound. T18's twin runs the same commitAcks a request at a
// time, so it cannot see a change to this order; these cases pin the order itself, one request each.
func TestDeliveryJournal_AckBatchKeepsEachCallsCheckOrder(t *testing.T) {
	t.Run("a cancelled context is answered before the gate", func(t *testing.T) {
		_, lock, journal := newTestDeliveryJournal(t)
		l := leaseEach(t, journal, 1)
		require.NoError(t, lock.Release())
		canceled, cancel := context.WithCancel(context.Background())
		cancel()
		err := journal.acknowledge(canceled, l[0].Delivery, l[0].ObservationID, core.Hash{})
		require.ErrorIs(t, err, context.Canceled)
		require.NotErrorIs(t, err, core.ErrDegraded)
	})

	t.Run("the gate is answered before validation", func(t *testing.T) {
		_, lock, journal := newTestDeliveryJournal(t)
		require.NoError(t, lock.Release())
		err := journal.acknowledge(context.Background(), "", "", core.Hash{})
		require.ErrorIs(t, err, core.ErrDegraded)
		require.NotErrorIs(t, err, core.ErrContract)
	})

	t.Run("an acknowledged delivery is answered without a check", func(t *testing.T) {
		_, _, journal := newTestDeliveryJournal(t)
		ctx := context.Background()
		l := leaseEach(t, journal, 2)
		require.NoError(t, journal.acknowledge(ctx, l[0].Delivery, l[0].ObservationID, core.Hash{}))
		require.NoError(t, bumpTestPositionCount(ackPositionPath(journal), deliveryAckChainSeed, deliveryAckChainDomain))
		before := readJournalFiles(t, journal)
		require.NoError(t, journal.acknowledge(ctx, l[0].Delivery, l[0].ObservationID, core.Hash{}),
			"an idempotent acknowledgement reads no file, so a corruption cannot refuse it")
		require.NoError(t, journalFault(journal), "and it poisons nothing")
		require.ErrorIs(t, journal.acknowledge(ctx, l[1].Delivery, l[1].ObservationID, core.Hash{}), core.ErrDegraded,
			"a new acknowledgement runs the check, which finds the corruption")
		require.Equal(t, before, readJournalFiles(t, journal), "nothing was appended or sealed")
	})

	t.Run("the check is answered before the entries bound", func(t *testing.T) {
		_, _, journal := newTestDeliveryJournal(t)
		ctx := context.Background()
		l := leaseEach(t, journal, 2)
		const size = 4 << 10
		ackNearCap(t, journal, deliveryLeaseMaxEntries, size)
		err := journal.acknowledge(ctx, l[0].Delivery, l[0].ObservationID, core.Hash{})
		require.ErrorIs(t, err, core.ErrBudget, "the acknowledgement journal is at its entries cap")
		require.NoError(t, journalFault(journal), "a refusal at the cap poisons nothing")
		require.NoError(t, os.Truncate(paths.Long(journal.ackPath), size-1))
		err = journal.acknowledge(ctx, l[1].Delivery, l[1].ObservationID, core.Hash{})
		require.ErrorIs(t, err, core.ErrDegraded, "the check finds the truncation before the bound is reached")
		require.NotErrorIs(t, err, core.ErrBudget)
	})
}

// T17 — design §6.2, §2.8, fix J-A3. An acknowledgement batch holds no lock a lease needs: while one
// holds its Sync, a delivery is accepted on the live path, through the journal accessor (which takes
// Lock.mu) and a lease batch of its own, and it is leased and returned before the acknowledgement's
// Sync is released. With acknowledge under Lock.mu, as it was before this stage, the Accept waits for
// the whole acknowledgement: its Write, Sync and seal.
func TestDeliveryJournal_AckBatchNeverDelaysALeaseBatch(t *testing.T) {
	root, lock, journal := newTestDeliveryJournal(t)
	l := leaseEach(t, journal, 1)
	ing := newIngest(root, config.Defaults(), logging.Nop(), nil, newFakeClock(epoch))
	t.Cleanup(func() { _ = ing.Close() })
	ing.journal = lock.openDeliveryJournal

	p := newAckProbe(journal)
	p.syncGate = newWALGate(t)
	ack := holdFirstAckBatch(t, p, ackOf(1, l[0], core.Hash{}))
	ackPosition := loadAckPosition(t, journal)

	nonce, err := ipc.NewDeliveryNonce()
	require.NoError(t, err)
	req := ipc.Request{Op: ipc.OpObserveTool, Session: "t17", TS: benchLeasedBaseTS, Nonce: nonce}
	line, err := ipc.EncodeRequest(req)
	require.NoError(t, err)
	accepted := make(chan error, 1)
	go func() { accepted <- ing.Accept(req, line) }()
	select {
	case err := <-accepted:
		require.NoError(t, err)
	case <-time.After(ingestACKWait):
		t.Fatal("a leased Accept waited for an acknowledgement batch's Sync")
	}
	require.False(t, ack.returned(), "the acknowledgement is still held at its Sync")
	require.Len(t, ing.ring, 1)
	queued := <-ing.ring
	require.True(t, queued.leased, "the delivery was leased while the acknowledgement batch was in flight")
	require.Equal(t, 2, admittedLeases(journal))
	require.Equal(t, ackPosition, loadAckPosition(t, journal), "the acknowledgement is still unsealed")
	require.False(t, acknowledgedWithin(t, journal, l[0].Delivery))

	p.syncGate.release()
	awaitAcks(t, ack)
	require.NoError(t, ack.err)
	require.True(t, journal.acknowledged(l[0].Delivery))
	require.Equal(t, ackPosition.Count+1, loadAckPosition(t, journal).Count)
}

// T17's converse — design §6.2 T17, §2.2 ("lease batch ∥ ack batch: allowed"), §2.8. The
// independence runs both ways: a lease batch holds no lock an acknowledgement needs either. While
// one holds its Sync, a drain's acknowledgement takes the journal through the accessor (which takes
// Lock.mu) and commits its own Write, Sync and seal, and it returns before the lease batch's Sync is
// released. The two pipelines have queues, writers and seals of their own, and j.st is taken only
// for the map reads and the admissions, never across I/O, so neither side waits on the other's
// device time. With a lease batch under Lock.mu, the accessor call would wait for the whole batch.
func TestDeliveryJournal_AckBatchIsNeverDelayedByALeaseBatch(t *testing.T) {
	_, lock, journal := newTestDeliveryJournal(t)
	l := leaseEach(t, journal, 1)
	req := testDeliveryRequest("t17 converse")
	lp, ap := newLeaseProbe(journal), newAckProbe(journal)
	lp.syncGate = newWALGate(t)
	leaseLead := holdFirstLeaseBatch(t, lp, leaseCall{delivery: leaseToken(100), session: "t17", request: req})
	leasePosition, err := journal.loadPosition()
	require.NoError(t, err)

	// The drainer's own sequence: the accessor, then acknowledge.
	acked := make(chan error, 1)
	go func() {
		j, aerr := lock.openDeliveryJournal()
		if aerr != nil {
			acked <- aerr
			return
		}
		acked <- j.acknowledge(context.Background(), l[0].Delivery, l[0].ObservationID, core.Hash{})
	}()
	select {
	case err := <-acked:
		require.NoError(t, err)
	case <-time.After(ingestACKWait):
		t.Fatal("an acknowledgement waited for a lease batch's Sync")
	}

	require.False(t, leaseLead.returned(), "the lease batch is still held at its Sync")
	require.True(t, acknowledgedWithin(t, journal, l[0].Delivery), "the acknowledgement is sealed and on the frontier")
	require.Equal(t, int32(1), ap.writes.Load(), "the acknowledgement made its own Write")
	require.Equal(t, int32(1), ap.syncs.Load(), "its own Sync")
	require.Equal(t, int32(1), ap.seals.Load(), "and its own seal")
	ackFile := readTestFile(t, journal.ackPath)
	require.Equal(t, ackLines(t, ackOf(0, l[0], core.Hash{})), string(ackFile))
	require.Equal(t, ackPositionOf(ackFile), loadAckPosition(t, journal))
	held, err := journal.loadPosition()
	require.NoError(t, err)
	require.Equal(t, leasePosition, held, "the lease batch is still unsealed")
	require.Equal(t, int32(0), lp.seals.Load())

	lp.syncGate.release()
	awaitAll(t, leaseLead)
	require.NoError(t, leaseLead.err)
	sealed, err := journal.loadPosition()
	require.NoError(t, err)
	require.Equal(t, leasePosition.Count+1, sealed.Count, "and it seals once it is released")
}

// ackStart is the state T18's two journals start from.
type ackStart struct {
	entries int   // placeholder acknowledgements (ackNearCap); 0 for an empty acknowledgement journal
	size    int64 // the acknowledgement journal's starting length, with entries > 0 and room 0
	room    int   // with entries > 0: start room and a half lines short of the bytes cap instead
}

// T18's scripts acknowledge ackPool leased deliveries, the first ackPreAcked of which are
// acknowledged before the batch.
const (
	ackPool     = 12
	ackPreAcked = 2
)

// newAckScript draws n acknowledge requests from rng over pool: acknowledgements of leased
// deliveries, new or already committed, under either of two roots; copies of the delivery the
// request just before names, so often a second acknowledgement of one delivery in one batch;
// deliveries never leased; another delivery's identity; malformed requests; cancelled contexts.
func newAckScript(rng *rand.Rand, n int, pool []deliveryLease) []ackCall {
	roots := []core.Hash{{}, testDeliveryRequest("t18 root")}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	calls := make([]ackCall, 0, n)
	for len(calls) < n {
		i := rng.IntN(len(pool))
		c := ackOf(len(calls)+1, pool[i], roots[rng.IntN(len(roots))])
		switch roll := rng.IntN(100); {
		case roll < 45:
		case roll < 60 && len(calls) > 0:
			prev := calls[len(calls)-1]
			c.delivery, c.obs = prev.delivery, prev.obs
		case roll < 68:
			c.delivery = leaseToken(900 + rng.IntN(4))
		case roll < 76:
			c.obs = pool[(i+1)%len(pool)].ObservationID
		case roll < 84:
			switch rng.IntN(3) {
			case 0:
				c.delivery = ""
			case 1:
				c.delivery = strings.Repeat("A", 64)
			default:
				c.obs = ""
			}
		case roll < 92:
			c.ctx = canceled
		}
		calls = append(calls, c)
	}
	return calls
}

func requireSameAckOutcome(t *testing.T, id int, got, want error) {
	t.Helper()
	require.Equalf(t, want == nil, got == nil, "acknowledgement %d: batched %v, sequential %v", id, got, want)
	for _, s := range leaseSentinels {
		require.Equalf(t, errors.Is(want, s), errors.Is(got, s),
			"acknowledgement %d: errors.Is(%v) differs: batched %v, sequential %v", id, s, got, want)
	}
}

// T18 — design §6.2, §2.8. An acknowledgement batch answers every request exactly as the same
// requests made one acknowledge call at a time, in queue order, would have been answered, with the
// same errors.Is results, and it leaves the acknowledgement journal the same bytes, the same final
// seal and the same frontier. Two cases are pinned outright: duplicate acknowledgements of one
// delivery in one batch, and an acknowledgement of a delivery that was never leased. Seeded scripts
// then mix those with acknowledgements an earlier batch committed, another delivery's identity,
// malformed requests and cancelled contexts, and two kinds of start reach the real entries cap and
// the real bytes cap in the middle of the batch.
func TestDeliveryJournal_AckBatchMatchesSequentialOutcomes(t *testing.T) {
	t.Run("duplicate acknowledgements in one batch", func(t *testing.T) {
		_, _, journal := newTestDeliveryJournal(t)
		l := leaseEach(t, journal, 3)
		p := newAckProbe(journal)
		p.syncGate = newWALGate(t)
		other := testDeliveryRequest("t18 other root")
		lead := holdFirstAckBatch(t, p, ackOf(0, l[0], core.Hash{}))
		runs := queueAcks(t, p,
			ackOf(1, l[1], core.Hash{}),
			ackOf(2, l[1], other), // the same delivery under another root
			ackOf(3, l[2], core.Hash{}),
			ackOf(4, l[1], core.Hash{}),
			ackOf(5, l[0], other), // batch 1's delivery, committed before this batch runs
		)
		p.syncGate.release()
		awaitAcks(t, append([]*ackRun{lead}, runs...)...)
		require.NoError(t, lead.err)
		for _, r := range runs {
			require.NoErrorf(t, r.err, "acknowledgement %d", r.id)
		}
		require.Equal(t, int32(2), p.writes.Load())
		file := readTestFile(t, journal.ackPath)
		require.Equal(t, ackLines(t, lead.ackCall, runs[0].ackCall, runs[2].ackCall), string(file),
			"one line per delivery, the first acknowledgement's, root and all: the copies join it")
		require.Equal(t, ackPositionOf(file), loadAckPosition(t, journal))
		require.Equal(t, 3, admittedAcks(journal))
		for _, k := range l {
			require.True(t, journal.acknowledged(k.Delivery))
		}
	})

	t.Run("an acknowledgement of a delivery never leased", func(t *testing.T) {
		_, _, journal := newTestDeliveryJournal(t)
		l := leaseEach(t, journal, 2)
		p := newAckProbe(journal)
		p.syncGate = newWALGate(t)
		lead := holdFirstAckBatch(t, p, ackOf(0, l[0], core.Hash{}))
		runs := queueAcks(t, p,
			ackCall{id: 1, delivery: leaseToken(900), obs: l[1].ObservationID},
			ackOf(2, l[1], core.Hash{}),
			ackCall{id: 3, delivery: leaseToken(901), obs: "unassigned"},
		)
		p.syncGate.release()
		awaitAcks(t, append([]*ackRun{lead}, runs...)...)
		require.NoError(t, lead.err)
		require.ErrorIs(t, runs[0].err, core.ErrContract, "a frontier record may not name an assignment that does not exist")
		require.NoError(t, runs[1].err, "and the refusal costs nothing else in its batch")
		require.ErrorIs(t, runs[2].err, core.ErrContract)
		require.NoError(t, journalFault(journal), "a contract refusal poisons nothing")
		require.Equal(t, ackLines(t, lead.ackCall, runs[1].ackCall), string(readTestFile(t, journal.ackPath)))
		require.False(t, journal.acknowledged(leaseToken(900)))
		require.True(t, journal.acknowledged(l[1].Delivery))
	})

	for seed := range uint64(8) {
		t.Run(fmt.Sprintf("seed %d", seed), func(t *testing.T) {
			runAckTwin(t, seed, ackStart{}, 24)
		})
	}
	for seed := range uint64(2) {
		t.Run(fmt.Sprintf("entries cap, seed %d", seed), func(t *testing.T) {
			runAckTwin(t, 100+seed, ackStart{entries: deliveryLeaseMaxEntries - 6, size: 4 << 10}, 16)
		})
	}
	t.Run("bytes cap", func(t *testing.T) {
		runAckTwin(t, 200, ackStart{entries: 1, room: 5}, 16)
	})
}

func runAckTwin(t *testing.T, seed uint64, start ackStart, n int) {
	ctx := context.Background()
	var base int64 // the acknowledgement journal's starting length
	open := func() (*deliveryJournal, []deliveryLease) {
		_, _, j := newTestDeliveryJournal(t)
		l := leaseEach(t, j, 1+ackPool)
		if start.entries > 0 {
			base = start.size
			if start.room > 0 {
				width := int64(len(ackLines(t, ackOf(0, l[0], core.Hash{}))))
				base = deliveryLeaseMaxBytes - int64(start.room)*width - width/2
			}
			ackNearCap(t, j, start.entries, base)
		}
		for _, k := range l[1 : 1+ackPreAcked] {
			require.NoError(t, j.acknowledge(ctx, k.Delivery, k.ObservationID, core.Hash{}))
		}
		return j, l
	}

	// The twin: the gate, then every request alone, in queue order, each a batch of one.
	twin, l := open()
	script := newAckScript(rand.New(rand.NewPCG(seed, uint64(n))), n, l[1:])
	gate := ackOf(0, l[0], core.Hash{})
	twinProbe := newAckProbe(twin)
	require.NoError(t, twin.acknowledge(ctx, gate.delivery, gate.obs, gate.root))
	want := make([]error, len(script))
	for k, c := range script {
		want[k] = twin.acknowledge(c.context(), c.delivery, c.obs, c.root)
	}

	// The batch: the same requests queued behind the gate's batch, so that they are cut together.
	j, _ := open()
	p := newAckProbe(j)
	p.syncGate = newWALGate(t)
	lead := holdFirstAckBatch(t, p, gate)
	runs := queueAcks(t, p, script...)
	p.syncGate.release()
	awaitAcks(t, append([]*ackRun{lead}, runs...)...)
	require.NoError(t, lead.err)
	for k, r := range runs {
		requireSameAckOutcome(t, r.id, r.err, want[k])
	}

	got, wantFile := readTestFile(t, j.ackPath), readTestFile(t, twin.ackPath)
	require.Len(t, got, len(wantFile))
	require.Equal(t, string(wantFile[base:]), string(got[base:]),
		"the batch appends exactly the bytes one call at a time appends")
	gotSeal, wantSeal := loadAckPosition(t, j), loadAckPosition(t, twin)
	require.Equal(t, wantSeal, gotSeal)
	require.Equal(t, int64(len(got)), gotSeal.Bytes)
	require.Equal(t, admittedAcks(twin), admittedAcks(j))
	for _, c := range script {
		require.Equalf(t, twin.acknowledged(c.delivery), j.acknowledged(c.delivery), "delivery %q", c.delivery)
	}
	if twinProbe.writes.Load() > 2 {
		require.Less(t, p.writes.Load(), twinProbe.writes.Load(), "the queued requests were not batched")
	}
	requireIdle(t, &j.ackQ)
}

// T19 — design §6.2, fix J-B1. acknowledged reads Lock.released only through owned, under Lock.mu,
// where Release writes it, and it reads the frontier under st, where an acknowledgement batch admits
// into it. Both are pinned for the race detector: acknowledged loops while a Release lands and after
// it has returned, and it loops while acknowledgement batches admit. A read moved out from under
// either mutex is a data race that -race reports here; without -race these cases check only the
// answers.
//
// WHAT THESE LOOPS MAY NOT DO IS SYNCHRONIZE WITH WHAT THEY RACE. Every channel send, mutex and
// atomic between the write and the loop's read is a happens-before edge, and an edge is exactly
// what hides the race. The first version of the case below had the test set an atomic flag once
// Release returned, which the loop read each pass; the mutation that drops Lock.mu from
// acknowledged then survived 3 runs of 3, because that flag ordered Release's write ahead of every
// later read. So the loop now publishes to the test (an atomic pass count, and its own results once
// it has stopped) and the test signals the loop only at the very end.
func TestDeliveryJournal_AcknowledgedIsRaceFreeWithRelease(t *testing.T) {
	t.Run("acknowledged loops through a Release", func(t *testing.T) {
		root, lock, journal := newTestDeliveryJournal(t)
		ctx := context.Background()
		l := leaseEach(t, journal, 1)
		require.NoError(t, journal.acknowledge(ctx, l[0].Delivery, l[0].ObservationID, core.Hash{}))

		// ackPass is what one pass of the loop saw. The loop owns results until it has stopped.
		type ackPass struct {
			acknowledged bool
			err          error
		}
		var passes atomic.Int64
		var results []ackPass
		stop, stopped := make(chan struct{}), make(chan struct{})
		go func() {
			defer close(stopped)
			for {
				select {
				case <-stop:
					return
				default:
				}
				p := ackPass{acknowledged: journal.acknowledged(l[0].Delivery)}
				p.err = journal.acknowledge(ctx, l[0].Delivery, l[0].ObservationID, core.Hash{})
				results = append(results, p)
				passes.Add(1)
			}
		}()
		var once sync.Once
		stopLoop := func() {
			once.Do(func() { close(stop) })
			awaitClosed(t, stopped, "the end of the acknowledged loop")
		}
		t.Cleanup(stopLoop)
		awaitPasses(t, &passes, 1)

		require.NoError(t, lock.Release())
		// Pass mark+1 ended after this read of mark, so every pass from mark+2 on began after
		// Release had returned, and each of those must find the journal released.
		mark := passes.Load()
		const passesAfterRelease = 8
		awaitPasses(t, &passes, mark+passesAfterRelease)
		stopLoop()

		require.GreaterOrEqual(t, len(results), int(mark)+passesAfterRelease)
		require.True(t, results[0].acknowledged, "the loop ran while the journal was open")
		require.NoError(t, results[0].err, "an idempotent acknowledgement before the Release")
		for i := int(mark) + 1; i < len(results); i++ {
			require.Falsef(t, results[i].acknowledged, "pass %d: a released journal said what is acknowledged", i+1)
			require.ErrorIsf(t, results[i].err, core.ErrDegraded, "pass %d: a released journal acknowledged", i+1)
		}
		require.NoFileExists(t, LockPath(root))
	})

	t.Run("acknowledged loops beside acknowledgement batches", func(t *testing.T) {
		_, _, journal := newTestDeliveryJournal(t)
		// An admission that drops st races a reader only while its batch is in flight, which is a
		// window per batch, not a state. Several readers and several waves of acknowledgements make
		// that window come round often; the mutation is caught in a run, not in every batch.
		const readers, wave, waves = 4, 32, 8
		l := leaseEach(t, journal, wave*waves)
		var passes atomic.Int64
		stop, stopped := make(chan struct{}), make(chan struct{}, readers)
		for range readers {
			go func() {
				defer func() { stopped <- struct{}{} }()
				for k := 0; ; k++ {
					select {
					case <-stop:
						return
					default:
					}
					_ = journal.acknowledged(l[k%len(l)].Delivery)
					passes.Add(1)
				}
			}()
		}
		var once sync.Once
		stopLoop := func() {
			once.Do(func() {
				close(stop)
				for range readers {
					select {
					case <-stopped:
					case <-time.After(ingestACKWait):
						t.Error("an acknowledged loop never ended")
						return
					}
				}
			})
		}
		t.Cleanup(stopLoop)
		awaitPasses(t, &passes, readers)

		p := newAckProbe(journal)
		for w := range waves {
			runs := make([]*ackRun, wave)
			for k := range runs {
				runs[k] = goAck(p, ackOf(w*wave+k+1, l[w*wave+k], core.Hash{}))
			}
			awaitAcks(t, runs...)
			for _, r := range runs {
				require.NoErrorf(t, r.err, "acknowledgement %d", r.id)
			}
		}
		stopLoop()
		for _, k := range l {
			require.Truef(t, journal.acknowledged(k.Delivery), "delivery %s", k.Delivery)
		}
		require.Equal(t, len(l), admittedAcks(journal))
	})
}

// awaitPasses waits until passes reaches at least n. The bound only turns a stalled loop into a
// failure.
func awaitPasses(t *testing.T, passes *atomic.Int64, n int64) {
	t.Helper()
	deadline := time.Now().Add(ingestACKWait)
	for passes.Load() < n {
		if time.Now().After(deadline) {
			t.Fatalf("the loop made %d passes, never %d", passes.Load(), n)
		}
		runtime.Gosched()
	}
}

// acknowledgedWithin is j.acknowledged(delivery), bounded: it fails the test when the call has not
// returned within ingestACKWait. That is what happens if an acknowledgement batch holds Lock.mu
// while a test holds the batch open, and the bound makes such a regression fail, not hang.
func acknowledgedWithin(t *testing.T, j *deliveryJournal, delivery string) bool {
	t.Helper()
	out := make(chan bool, 1)
	go func() { out <- j.acknowledged(delivery) }()
	select {
	case ok := <-out:
		return ok
	case <-time.After(ingestACKWait):
		t.Fatalf("acknowledged(%s) never returned: something holds Lock.mu", delivery)
		return false
	}
}

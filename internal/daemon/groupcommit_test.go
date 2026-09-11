package daemon

import (
	"bytes"
	"fmt"
	"math/rand/v2"
	"runtime"
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

// The group-commit queue tests (design §6.2 T1–T3). None of them reads the clock: every ordering
// they need is built from channels a commit blocks on and from the queue's own length.

// gcTestItem is one request in these tests. It carries its own result the way every pipeline's
// item does: err starts as the failure errNotCommitted (the J-A2 convention), and only a commit
// may replace it.
type gcTestItem struct {
	id   int
	size int
	gid  uint64 // the goroutine that submitted the request
	err  error
	// batch is the index of the batch whose commit resolved the item (-1 until one does), and
	// commits counts how many commits resolved it.
	batch   int
	commits int
}

// newGCTestItem must be called on the goroutine that will submit the item.
func newGCTestItem(id, size int) *gcTestItem {
	return &gcTestItem{id: id, size: size, gid: goid(), err: errNotCommitted, batch: -1}
}

func gcTestSize(it *gcTestItem) int { return it.size }

// gcBody is what a test's commit does once the batch has been recorded: resolve it, block, or
// panic. idx is the batch's index in the recorder.
type gcBody func(idx int, batch []*gcTestItem)

// resolveAll marks every member of batch committed by batch idx.
func resolveAll(idx int, batch []*gcTestItem) {
	for _, it := range batch {
		it.commits++
		it.batch = idx
		it.err = nil
	}
}

// gcBatch is one commit as the recorder saw it.
type gcBatch struct {
	ids []int
	// leader is the id of the request whose commit function ran this batch.
	leader int
	// inline is true when commit ran on the goroutine that submitted the batch's head.
	inline bool
}

// gcRecorder is the event log of one queue under test. batches is written only inside commit and
// deliberately has no lock of its own, so the race detector checks the queue's promise that
// commits never overlap and that each one happens-before the next. overlaps counts the same
// violation without the race detector.
type gcRecorder struct {
	batches  []gcBatch
	inCommit atomic.Int32
	overlaps atomic.Int32
}

// commitFor returns the commit function that request id passes to run: it records the batch and
// then runs body (resolveAll when body is nil).
func (r *gcRecorder) commitFor(id int, body gcBody) func([]*gcTestItem) {
	if body == nil {
		body = resolveAll
	}
	return func(batch []*gcTestItem) {
		if r.inCommit.Add(1) != 1 {
			r.overlaps.Add(1)
		}
		defer r.inCommit.Add(-1)
		b := gcBatch{leader: id, inline: batch[0].gid == goid()}
		for _, it := range batch {
			b.ids = append(b.ids, it.id)
		}
		r.batches = append(r.batches, b)
		body(len(r.batches)-1, batch)
	}
}

// ids returns every recorded batch's member ids, in commit order.
func (r *gcRecorder) ids() [][]int {
	out := make([][]int, len(r.batches))
	for i, b := range r.batches {
		out[i] = b.ids
	}
	return out
}

// batchOf maps every recorded member id to the index of the batch that contained it, failing if a
// request appears in more than one batch.
func (r *gcRecorder) batchOf(t *testing.T) map[int]int {
	t.Helper()
	at := map[int]int{}
	for i, b := range r.batches {
		for _, id := range b.ids {
			prev, dup := at[id]
			require.Falsef(t, dup, "request %d was cut into batch %d and again into batch %d", id, prev, i)
			at[id] = i
		}
	}
	return at
}

// gcTestPanic is the value a test's commit panics with, naming the leader that threw it.
type gcTestPanic struct{ leader int }

// gcOutcome is how one submitted request ended on its own goroutine.
type gcOutcome struct {
	item      *gcTestItem
	recovered any // what run panicked with on this goroutine; nil when it returned
}

// submit runs one request on a new goroutine and reports how it ended on out.
func submit(q *groupQueue[*gcTestItem], rec *gcRecorder, id, size int, body gcBody, out chan<- gcOutcome) {
	go func() {
		it := newGCTestItem(id, size)
		defer func() { out <- gcOutcome{item: it, recovered: recover()} }()
		q.run(it, rec.commitFor(id, body))
	}()
}

// collect receives n outcomes, keyed by request id. A panic that panicOK does not expect (nil
// expects none) fails the test as soon as it arrives, rather than after every other request has
// had the chance to hang behind it.
func collect(t *testing.T, out <-chan gcOutcome, n int, panicOK func(gcOutcome) bool) map[int]gcOutcome {
	t.Helper()
	got := make(map[int]gcOutcome, n)
	for range n {
		o := <-out
		if o.recovered != nil && (panicOK == nil || !panicOK(o)) {
			t.Fatalf("request %d: run panicked unexpectedly: %v", o.item.id, o.recovered)
		}
		_, dup := got[o.item.id]
		require.Falsef(t, dup, "request %d returned twice", o.item.id)
		got[o.item.id] = o
	}
	return got
}

// waitQueued blocks until q holds at least n queued requests, and returns how many it holds. It
// waits on the queue's own state, never on the clock.
func waitQueued[T any](q *groupQueue[T], n int) int {
	for {
		q.mu.Lock()
		got := len(q.queue)
		q.mu.Unlock()
		if got >= n {
			return got
		}
		runtime.Gosched()
	}
}

// requireIdle asserts that nothing is queued and no batch is in flight.
func requireIdle[T any](t *testing.T, q *groupQueue[T]) {
	t.Helper()
	q.mu.Lock()
	leading, queued := q.leading, len(q.queue)
	q.mu.Unlock()
	require.False(t, leading, "leadership must be released once the queue drains")
	require.Zero(t, queued)
}

// goid returns the calling goroutine's id, parsed from the header line runtime.Stack writes
// ("goroutine 18 [running]:"). The tests use it only to show that each batch is committed on the
// goroutine whose request heads it, which is to say that the queue never hands work to a goroutine
// of its own.
func goid() uint64 {
	var buf [64]byte
	s := buf[:runtime.Stack(buf[:], false)]
	s = bytes.TrimPrefix(s, []byte("goroutine "))
	if i := bytes.IndexByte(s, ' '); i > 0 {
		s = s[:i]
	}
	id, err := strconv.ParseUint(string(s), 10, 64)
	if err != nil {
		panic(fmt.Sprintf("goid: cannot parse %q", s))
	}
	return id
}

// holdFirstBatch submits request 0 on an idle queue: it leads at once, and its commit records the
// batch and then blocks until the returned release function is called, so the tests can line
// other requests up behind it in a known order. It returns once request 0's commit is running.
func holdFirstBatch(q *groupQueue[*gcTestItem], rec *gcRecorder, out chan<- gcOutcome) (release func()) {
	entered, gate := make(chan struct{}), make(chan struct{})
	submit(q, rec, 0, 1, func(idx int, batch []*gcTestItem) {
		close(entered)
		<-gate
		resolveAll(idx, batch)
	}, out)
	<-entered
	return func() { close(gate) }
}

// queueInOrder submits the requests ids[i] with sizes[i], one at a time, and waits after each one
// until it is queued, so the queue's FIFO order is exactly ids. The queue must be empty, with a
// batch in flight.
func queueInOrder(t *testing.T, q *groupQueue[*gcTestItem], rec *gcRecorder, ids, sizes []int, body gcBody, out chan<- gcOutcome) {
	t.Helper()
	for k, id := range ids {
		submit(q, rec, id, sizes[k], body, out)
		require.Equal(t, k+1, waitQueued(q, k+1), "request %d must queue behind the batch in flight", id)
	}
}

// T1.
func TestGroupQueue_FIFOBatchesEachRequestCommittedOnce(t *testing.T) {
	t.Run("batches are the longest FIFO prefixes within the caps", func(t *testing.T) {
		const maxN, maxBytes = 4, 10
		q := &groupQueue[*gcTestItem]{maxN: maxN, maxBytes: maxBytes, size: gcTestSize}
		rec := &gcRecorder{}
		out := make(chan gcOutcome, 16)

		// Request 0 finds the queue idle: it is a batch of one, committed inline by its own
		// goroutine. Its commit holds the batch open while 1..12 queue up behind it.
		release := holdFirstBatch(q, rec, out)
		ids := []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12}
		sizes := []int{3, 3, 3, 3, 11, 1, 1, 1, 1, 1, 2, 0}
		queueInOrder(t, q, rec, ids, sizes, nil, out)
		release()
		got := collect(t, out, len(ids)+1, nil)

		// 1..3: the byte cap stops the prefix before 4 (9+3 > 10). 4: 5 would add 11. 5: over
		// the byte cap on its own, admitted because it is the head. 6..9: the count cap stops
		// the prefix before 10. 10..12: the queue runs out.
		want := [][]int{{0}, {1, 2, 3}, {4}, {5}, {6, 7, 8, 9}, {10, 11, 12}}
		require.Equal(t, want, rec.ids())
		for i, b := range rec.batches {
			require.Equalf(t, b.ids[0], b.leader, "batch %d must be committed by its head's own commit", i)
			require.Truef(t, b.inline, "batch %d must be committed on its head's goroutine", i)
		}
		for i, members := range want {
			for _, id := range members {
				it := got[id].item
				require.Equalf(t, 1, it.commits, "request %d must be committed exactly once", id)
				require.Equalf(t, i, it.batch, "request %d", id)
				require.NoErrorf(t, it.err, "request %d", id)
			}
		}
		require.Zero(t, rec.overlaps.Load())
		requireIdle(t, q)
	})

	t.Run("concurrent callers are each committed once by one serial leader per batch", func(t *testing.T) {
		const workers, perWorker, maxN, maxBytes = 16, 64, 5, 16
		q := &groupQueue[*gcTestItem]{maxN: maxN, maxBytes: maxBytes, size: gcTestSize}
		rec := &gcRecorder{}
		out := make(chan gcOutcome, workers*perWorker)
		for w := range workers {
			go func() {
				rng := rand.New(rand.NewPCG(uint64(w), uint64(perWorker)))
				for k := range perWorker {
					size := rng.IntN(maxBytes/2 + 1)
					if rng.IntN(maxBytes) == 0 {
						size = maxBytes + 1 + rng.IntN(maxBytes) // over the cap: must commit alone
					}
					it := newGCTestItem(w*perWorker+k, size)
					func() {
						defer func() { out <- gcOutcome{item: it, recovered: recover()} }()
						q.run(it, rec.commitFor(it.id, nil))
					}()
				}
			}()
		}
		got := collect(t, out, workers*perWorker, nil)

		at := rec.batchOf(t)
		require.Len(t, at, workers*perWorker, "every request must be cut into exactly one batch")
		for id, o := range got {
			require.Equalf(t, 1, o.item.commits, "request %d must be committed exactly once", id)
			require.Equalf(t, at[id], o.item.batch, "request %d must be resolved by the batch that cut it", id)
			require.NoErrorf(t, o.item.err, "request %d", id)
		}
		for i, b := range rec.batches {
			require.NotEmptyf(t, b.ids, "batch %d", i)
			require.LessOrEqualf(t, len(b.ids), maxN, "batch %d exceeds the count cap", i)
			if len(b.ids) > 1 {
				sum := 0
				for _, id := range b.ids {
					sum += got[id].item.size
				}
				require.LessOrEqualf(t, sum, maxBytes, "batch %d exceeds the byte cap without being a lone head", i)
			}
			require.Equalf(t, b.ids[0], b.leader, "batch %d must be committed by its head's own commit", i)
			require.Truef(t, b.inline, "batch %d must be committed on its head's goroutine", i)
		}
		require.Zero(t, rec.overlaps.Load(), "commits must never overlap")
		requireIdle(t, q)
	})

	t.Run("the zero value commits one request per batch", func(t *testing.T) {
		const workers, perWorker = 8, 16
		q := &groupQueue[*gcTestItem]{}
		rec := &gcRecorder{}
		out := make(chan gcOutcome, workers*perWorker)
		for w := range workers {
			go func() {
				for k := range perWorker {
					it := newGCTestItem(w*perWorker+k, 0)
					func() {
						defer func() { out <- gcOutcome{item: it, recovered: recover()} }()
						q.run(it, rec.commitFor(it.id, nil))
					}()
				}
			}()
		}
		got := collect(t, out, workers*perWorker, nil)
		require.Len(t, rec.batches, workers*perWorker)
		for i, b := range rec.batches {
			require.Lenf(t, b.ids, 1, "batch %d", i)
			require.Truef(t, b.inline, "batch %d", i)
		}
		for id, o := range got {
			require.Equalf(t, 1, o.item.commits, "request %d", id)
			require.NoErrorf(t, o.item.err, "request %d", id)
		}
		requireIdle(t, q)
	})
}

// T2 — the J-A1 regression. A commit panics while requests are queued behind its batch, so the
// deferred handoff has to pick a next leader during the panic; then 1 000 more requests, a third
// of whose batches also panic, must all complete.
func TestGroupQueue_PanicInCommitHandsOffWithoutDeadlock(t *testing.T) {
	const maxN, maxBytes = 8, 64
	q := &groupQueue[*gcTestItem]{maxN: maxN, maxBytes: maxBytes, size: gcTestSize}
	rec := &gcRecorder{}

	// The deterministic shape: 0 holds a batch open while 1..4 queue; 1 leads [1 2 3 4] and its
	// commit blocks while 5..7 queue; then that commit panics. The handoff must release 2..4 with
	// their default failure and hand leadership to 5 over a non-empty queue.
	out := make(chan gcOutcome, 8)
	release := holdFirstBatch(q, rec, out)
	entered, gate := make(chan struct{}), make(chan struct{})
	panicking := func(int, []*gcTestItem) {
		close(entered)
		<-gate
		panic(gcTestPanic{leader: 1})
	}
	queueInOrder(t, q, rec, []int{1, 2, 3, 4}, []int{1, 1, 1, 1}, panicking, out)
	release()
	<-entered
	queueInOrder(t, q, rec, []int{5, 6, 7}, []int{1, 1, 1}, nil, out)
	close(gate)
	got := collect(t, out, 8, func(o gcOutcome) bool { return o.item.id == 1 && o.recovered == gcTestPanic{leader: 1} })

	require.Equal(t, [][]int{{0}, {1, 2, 3, 4}, {5, 6, 7}}, rec.ids())
	require.Equal(t, gcTestPanic{leader: 1}, got[1].recovered, "the panic must continue on the leader's goroutine")
	for _, id := range []int{1, 2, 3, 4} {
		require.ErrorIsf(t, got[id].item.err, errNotCommitted, "request %d belonged to the batch that panicked", id)
		require.Zerof(t, got[id].item.commits, "request %d", id)
	}
	for _, id := range []int{0, 5, 6, 7} {
		require.NoErrorf(t, got[id].item.err, "request %d", id)
		require.Equalf(t, 1, got[id].item.commits, "request %d", id)
		require.Nilf(t, got[id].recovered, "request %d", id)
	}
	requireIdle(t, q)

	// 1 000 further requests, all submitted at once. Every batch whose index is a multiple of 3
	// panics before resolving anything.
	const further = 1000
	first := len(rec.batches)
	sometimesPanics := func(idx int, batch []*gcTestItem) {
		if idx%3 == 0 {
			panic(gcTestPanic{leader: batch[0].id})
		}
		resolveAll(idx, batch)
	}
	more := make(chan gcOutcome, further)
	for k := range further {
		submit(q, rec, 100+k, 1+k%maxN, sometimesPanics, more)
	}
	done := collect(t, more, further, func(o gcOutcome) bool { return o.recovered == gcTestPanic{leader: o.item.id} })

	at := rec.batchOf(t)
	panicked := 0
	for id, o := range done {
		idx, ok := at[id]
		require.Truef(t, ok, "request %d completed without being cut into a batch", id)
		require.GreaterOrEqual(t, idx, first)
		if idx%3 == 0 {
			require.ErrorIsf(t, o.item.err, errNotCommitted, "request %d belonged to panicking batch %d", id, idx)
			require.Zerof(t, o.item.commits, "request %d", id)
			if rec.batches[idx].leader == id {
				require.Equalf(t, gcTestPanic{leader: id}, o.recovered, "request %d led panicking batch %d", id, idx)
				panicked++
			} else {
				require.Nilf(t, o.recovered, "request %d only followed batch %d", id, idx)
			}
			continue
		}
		require.Nilf(t, o.recovered, "request %d", id)
		require.NoErrorf(t, o.item.err, "request %d", id)
		require.Equalf(t, 1, o.item.commits, "request %d", id)
		require.Equalf(t, idx, o.item.batch, "request %d", id)
	}
	wantPanics := 0
	for idx := first; idx < len(rec.batches); idx++ {
		if idx%3 == 0 {
			wantPanics++
		}
	}
	require.Positive(t, wantPanics, "the further requests must include at least one panicking batch")
	require.Equal(t, wantPanics, panicked, "each panicking batch's panic must surface on its leader, once")
	require.Zero(t, rec.overlaps.Load(), "commits must never overlap")
	requireIdle(t, q)
}

// T3 — the J-A2 regression. A commit that returns early or panics leaves every member it did not
// resolve failed, leader and followers alike; only an item a commit resolved after its durability
// point may report success. That Accept never returns nil for such a batch joins this test with
// the WAL stage, which is the first caller of the queue.
func TestGroupQueue_UnassignedResultStaysAFailure(t *testing.T) {
	members := []int{1, 2, 3, 4}
	resolvePrefix := func(idx int, batch []*gcTestItem) { resolveAll(idx, batch[:2]) }
	cases := []struct {
		name     string
		body     gcBody
		resolved int // how many members, from the head, the commit resolves
		panics   bool
	}{
		{"commit returns early", func(int, []*gcTestItem) {}, 0, false},
		{"commit resolves a prefix and returns", resolvePrefix, 2, false},
		{"commit panics", func(int, []*gcTestItem) { panic(gcTestPanic{leader: 1}) }, 0, true},
		{"commit resolves a prefix and panics", func(idx int, batch []*gcTestItem) {
			resolvePrefix(idx, batch)
			panic(gcTestPanic{leader: 1})
		}, 2, true},
		{"commit resolves every member (control)", resolveAll, len(members), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			q := &groupQueue[*gcTestItem]{maxN: groupCommitMaxRequests, maxBytes: journalGroupCommitMaxBytes, size: gcTestSize}
			rec := &gcRecorder{}
			out := make(chan gcOutcome, len(members)+1)
			release := holdFirstBatch(q, rec, out)
			queueInOrder(t, q, rec, members, []int{1, 1, 1, 1}, tc.body, out)
			release()
			got := collect(t, out, len(members)+1, func(o gcOutcome) bool {
				return tc.panics && o.item.id == 1 && o.recovered == gcTestPanic{leader: 1}
			})
			require.Equal(t, [][]int{{0}, members}, rec.ids())
			require.NoError(t, got[0].item.err)

			for k, id := range members {
				it := got[id].item
				if k < tc.resolved {
					require.NoErrorf(t, it.err, "request %d was resolved after its durability point", id)
					require.Equalf(t, 1, it.commits, "request %d", id)
					continue
				}
				require.ErrorIsf(t, it.err, errNotCommitted, "request %d was never resolved, so it must stay failed", id)
				require.Zerof(t, it.commits, "request %d", id)
				require.Equalf(t, -1, it.batch, "request %d", id)
			}
			if tc.panics {
				require.Equal(t, gcTestPanic{leader: 1}, got[1].recovered)
			}

			// The queue survives the batch: the next request is a batch of one, committed inline.
			after := make(chan gcOutcome, 1)
			submit(q, rec, 5, 1, nil, after)
			last := collect(t, after, 1, nil)[5].item
			require.NoError(t, last.err)
			require.Equal(t, 2, last.batch)
			require.True(t, rec.batches[2].inline)
			requireIdle(t, q)
		})
	}
}

package daemon

import (
	"errors"
	"sync"
)

// This file is the one queue primitive SP20-D1's three durable pipelines are built on: the WAL, the
// delivery-lease journal and the acknowledgement journal (design §2.3). Nothing uses it yet; each
// pipeline adopts it in its own stage.
//
// It is a leader/follower group commit:
//
//   - A request that finds no batch in flight becomes the LEADER. It cuts the longest FIFO prefix
//     of the queue that fits the caps (its own request is always that prefix's head), runs commit
//     on the batch inline on its own goroutine, and then hands leadership to the next queued
//     request, if there is one.
//   - A request that finds a batch in flight becomes a FOLLOWER. It parks on its own channel until
//     either its result is final (a leader committed a batch that contained it) or it has been
//     handed leadership (it is now the head of the queue and commits the next batch itself).
//
// NOTHING WAITS FOR COMPANY. There are no timers, no linger and no goroutines, long-lived or
// otherwise. A batch is whatever queued up while the batch before it was committing, so an
// isolated request is a batch of one that its own goroutine commits at once, and all it pays over
// calling commit directly is two acquisitions of an uncontended leaf mutex and a few small
// allocations. Under load the batches grow by themselves, because each commit lasts exactly as
// long as the next batch has to accumulate.
//
// A LEADER COMMITS EXACTLY ONE BATCH, the one headed by its own request, and then hands over to the
// FIFO head. Nobody starves: a request waits for at most the batch in flight plus the batches
// ahead of it at the caps, and no goroutine is ever conscripted into committing other callers'
// work after its own is done.
//
// RESULTS DEFAULT TO FAILURE (the J-A2 fix). The queue never reads or writes a result. Every caller
// initialises its item's result to a failure (errNotCommitted, or its pipeline's own error) BEFORE
// it calls run, and commit replaces that failure with success only once the durability point that
// covers the item has passed. A commit that returns early, panics, or skips an item therefore
// leaves that item failed. A result that defaulted to its zero value would instead report success
// for bytes that were never written, and the ACK would go out for them.
//
// PANICS. run defers the handoff, so it runs even when commit panics: the batch's other members
// are released holding whatever commit had written (their default failure, unless commit had
// already resolved them), the next leader is woken, and the panic then continues on the leader's
// own goroutine. The queue does not recover it. A journal pipeline's commit recovers for itself and
// poisons its handle (design §2.5). The handoff's own critical section cannot panic; that is the
// J-A1 fix, described at handoff.
//
// LOCKING. q.mu guards the queue and the leading flag and nothing else. It is a leaf lock: it is
// never held together with another lock, never across I/O, and never while caller code runs (a
// request's size is taken before q.mu is, and commit runs after it is released).

// The caps of design §2.3. A batch holds at most groupCommitMaxRequests requests whose sizes, as
// estimated by the pipeline, sum to at most the pipeline's byte cap. The head of the queue is
// always admitted whatever its estimate, so an oversize request commits alone rather than never.
const (
	// groupCommitMaxRequests is every pipeline's cap on requests per batch. It bounds how many
	// callers one failed commit fails with it, and how many followers one handoff releases.
	groupCommitMaxRequests = 512

	// walGroupCommitMaxBytes caps one WAL batch (design §2.4): every line in it, across all of its
	// sessions, plus one terminator per line.
	walGroupCommitMaxBytes = 4 << 20 // 4 MiB

	// journalGroupCommitMaxBytes caps one delivery-lease batch and one acknowledgement batch
	// (design §2.6, §2.8), each counted in canonical journal lines.
	journalGroupCommitMaxBytes = 1 << 20 // 1 MiB
)

// errNotCommitted is the failure a group-commit result starts as, for a pipeline that has no more
// specific error of its own. Finding it after run returns means that no commit ever resolved the
// item: its batch's commit returned early, panicked, or skipped it. It is never a success, so a
// caller that surfaces it NAKs and the client's spool keeps the delivery.
var errNotCommitted = errors.New("daemon: group commit: request was not committed")

// gcReq is one request's place in a groupQueue.
type gcReq[T any] struct {
	item T
	// size is the queue's estimate for item, taken once before q.mu is acquired.
	size int
	// done is closed exactly once, by the handoff of the batch before this request's turn: either
	// "your result is final" (lead is false) or "you lead the next batch" (lead is true).
	done chan struct{}
	// lead is written under q.mu before done is closed, and read only after <-done.
	lead bool
}

// groupQueue is a leader/follower group-commit queue (see the file comment). Its zero value is
// ready to use and commits one request per batch; a pipeline sets maxN, maxBytes and size when it
// constructs the queue and never changes them afterwards.
type groupQueue[T any] struct {
	mu      sync.Mutex // guards queue and leading ONLY; never held across I/O or caller code
	queue   []*gcReq[T]
	leading bool // a batch is being cut or committed; false only while the queue is empty

	// maxN caps the requests in one batch and maxBytes the sum of their sizes. A value that is not
	// positive admits only the head.
	maxN     int
	maxBytes int
	// size is a pipeline's upper-bound estimate of one request's bytes in a batch; nil counts
	// every request as zero bytes, so only maxN applies. A negative estimate counts as zero.
	size func(T) int
}

// run submits item and returns once item's result is final: either a commit has resolved it, or
// the batch that contained it ended without doing so, in which case the result is still the
// failure the caller initialised it to.
//
// commit receives one batch of items in FIFO order, headed by the leader's own item. It runs on the
// leader's goroutine with no queue lock held, it is the only code that may write the batch's
// results, and each of those writes happens-before the run call that owns the item returns. Every
// caller passes its own commit, and only the leader's is called; a pipeline passes the same method
// value from every call site, so which caller leads never matters to it. commit must not call run
// on the same queue: its own batch has not finished, so the inner request would wait for itself.
func (q *groupQueue[T]) run(item T, commit func([]T)) {
	r := &gcReq[T]{item: item, size: q.sizeOf(item), done: make(chan struct{})}
	q.mu.Lock()
	q.queue = append(q.queue, r)
	if q.leading {
		q.mu.Unlock()
		<-r.done
		if !r.lead {
			return
		}
		// Handed leadership: r is q.queue[0], because requests only ever append and only the
		// leader cuts, and leading stayed true across the handoff that woke r.
		q.mu.Lock()
	} else {
		// leading is false only while the queue is empty, so r is q.queue[0].
		q.leading = true
	}
	batch := q.cutLocked()
	q.mu.Unlock()
	defer q.handoff(batch) // runs even if commit panics
	commit(itemsOf(batch))
}

// sizeOf is q.size(item), with a nil estimator or a negative estimate counted as zero. run calls it
// before taking q.mu, so a slow or panicking estimator can never hold the queue's lock.
func (q *groupQueue[T]) sizeOf(item T) int {
	if q.size == nil {
		return 0
	}
	if n := q.size(item); n > 0 {
		return n
	}
	return 0
}

// cutLocked removes and returns the longest FIFO prefix of the queue that fits the caps: at most
// maxN requests whose sizes sum to at most maxBytes, except that the head is always admitted. The
// comparison is written so that it cannot overflow whatever the sizes are. q.mu must be held and
// the queue must not be empty.
func (q *groupQueue[T]) cutLocked() []*gcReq[T] {
	n, total := 1, q.queue[0].size
	for n < len(q.queue) && n < q.maxN && total <= q.maxBytes && q.queue[n].size <= q.maxBytes-total {
		total += q.queue[n].size
		n++
	}
	batch := make([]*gcReq[T], n)
	copy(batch, q.queue)
	rest := copy(q.queue, q.queue[n:])
	clear(q.queue[rest:]) // drop the cut requests' pointers from the backing array
	q.queue = q.queue[:rest]
	return batch
}

// handoff ends a batch. It releases the batch's other members, and it either wakes the next leader
// or, when nothing is queued, clears leading so that the next request to arrive leads. run defers
// it, so it runs even when commit panics.
//
// The head of the queue is chosen and marked in two statements, never in one tuple assignment:
// `next, next.lead = q.queue[0], true` evaluates the pointer behind next.lead BEFORE next is
// assigned, so it writes through a nil pointer and panics inside this deferred call with q.mu
// held, and every later request then blocks on q.mu forever (J-A1). Nothing between Lock and
// Unlock here can panic.
func (q *groupQueue[T]) handoff(batch []*gcReq[T]) {
	q.mu.Lock()
	var next *gcReq[T]
	if len(q.queue) > 0 {
		next = q.queue[0]
		next.lead = true
	} else {
		q.leading = false
	}
	q.mu.Unlock()
	for _, b := range batch[1:] {
		close(b.done)
	}
	if next != nil {
		close(next.done)
	}
}

// itemsOf returns the items of batch, in order, for commit.
func itemsOf[T any](batch []*gcReq[T]) []T {
	items := make([]T, len(batch))
	for i, r := range batch {
		items[i] = r.item
	}
	return items
}

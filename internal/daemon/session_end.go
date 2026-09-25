package daemon

import (
	"context"
	"sync"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/ipc"
)

// Asynchronous SessionEnd (V6 close-out C1.15).
//
// Claude Code gives a plugin's SessionEnd hooks ONE SHARED budget of 1.5 s and cancels a hook still
// running when it runs out; a timeout set on a plugin-provided hook does not raise it (the hooks
// reference, and the packaging lane's two live sessions, where every flush hook ended "Hook
// cancelled"). The flush route used to do the whole of the session's end before it answered: wait for
// the session's queued deliveries (up to settleSessionLimit), SessionEnd with its GC (up to the
// observer's gcDeadline), the marker, the sketches and a full drain. The host cancelled every one.
//
// The flush is now answered as soon as it is DURABLE, which is exactly what an observe event's ACK
// promises: its line is appended to the session's WAL and synced, and it is leased (acceptSessionEnd,
// ingest.acceptDurable). The session is also on record as needing recovery by then: the marker the
// end clears once it has finished, which a caller holding the answer can wait on. The session is then
// ended on a goroutine of its own (startSessionEnd), which runs the same work the route always ran
// (endSession), still ordered after every earlier arrival of the session (settleSession, now bounded
// by the flush's own arrival), and which acknowledges the flush on the committed frontier once
// SessionEnd has run.
//
// Nothing is lost by answering first, and nothing depends on the hook for correctness (Qompack.md
// §8.2): a hook the host cancels after its answer changes nothing, because the daemon's work never ran
// on the hook's behalf; a daemon that dies before the end has run leaves the flush line unacknowledged
// in its WAL, and the next drain — Stop's own, or a restarted daemon's startup drain — replays it
// (drainDispatch -> flushRoute); a hook that missed the ACK deadline and spooled a copy of the flush
// has that copy absorbed through the flush's own lease once it is acknowledged. A caller that asks for
// the end's own answer (a Reply request: an older hook client, an operator, a test) still waits for it.

// sessionEndAbandonAfter is how long Stop still waits for a session end after cancelling it, before
// abandoning one that ignores cancellation. It is the bound Stop gives the one other kind of goroutine
// it joins after cancelling, a verbatim prompt capture (promptAbandonAfter): every step of a session
// end answers its context, so it returns within that once cancelled.
const sessionEndAbandonAfter = promptReplyDeadline

// counterSessionEndRefused counts flushes that arrived while Stop was already joining the session
// ends, so no end was started for them in this process. Each one's line is durable in its session's
// WAL, and Stop's own drain or the next daemon's startup drain replays it.
const counterSessionEndRefused = "l0_session_end_refused"

// sessionEnds is the set of session ends running on goroutines of their own, and the lifetime they
// run under: New creates it and only Stop ends it (stopSessionEnds), after giving the ends in flight
// a bounded window to finish.
//
// It counts the ends in flight itself, rather than with a sync.WaitGroup: a WaitGroup may not be
// waited on while an Add from zero can race the wait, and a flush can start an end at any moment
// someone waits for the ends to be idle (a Reply caller's test, an operator). idle is closed while
// none is running and replaced by the first end that starts, so a wait never blocks a start and never
// leaks a goroutine when its context ends first.
type sessionEnds struct {
	mu      sync.Mutex
	closed  bool
	running int
	idle    chan struct{}
	ctx     context.Context
	cancel  context.CancelFunc
}

func newSessionEnds() *sessionEnds {
	// Background, not any caller's context: an end must outlive the hook's request, and only Stop may
	// cancel it.
	ctx, cancel := context.WithCancel(context.Background())
	return &sessionEnds{ctx: ctx, cancel: cancel}
}

// begin counts one more end in flight, and reports false, counting nothing, once Stop has closed the
// gate.
func (e *sessionEnds) begin() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return false
	}
	if e.running == 0 {
		e.idle = make(chan struct{})
	}
	e.running++
	return true
}

// end counts one end finished, and releases every waiter once none is left.
func (e *sessionEnds) end() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.running--
	if e.running == 0 {
		close(e.idle)
	}
}

// close closes the gate: no end begins after it.
func (e *sessionEnds) close() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.closed = true
}

// wait blocks until no end is running, and reports whether it got there before ctx ended.
func (e *sessionEnds) wait(ctx context.Context) bool {
	e.mu.Lock()
	if e.running == 0 {
		e.mu.Unlock()
		return true
	}
	idle := e.idle
	e.mu.Unlock()
	select {
	case <-idle:
		return true
	case <-ctx.Done():
		return false
	}
}

// acceptSessionEnd makes a flush durable before it is answered — its exact line in its session's WAL
// and its lease, as ingest.Accept does for an observe event — and returns the accepted delivery.
// durable is false, with no error, when the mode does not record: nothing is made durable then, and
// the session's end runs from memory alone, exactly as it always did in that mode. An error means the
// flush is not acknowledged: the hook client spools it instead, and a drain replays it.
func (d *daemon) acceptSessionEnd(req ipc.Request) (own job, durable bool, err error) {
	if !d.monitor.Mode().MayRecord() {
		return job{}, false, nil
	}
	line, err := ipc.EncodeRequest(req)
	if err != nil {
		return job{}, false, err
	}
	own, err = d.ing.acceptDurable(req, line)
	if err != nil {
		return job{}, false, err
	}
	return own, true, nil
}

// startSessionEnd ends req's session on a goroutine of its own and returns the channel the end's
// answer arrives on; it is buffered, so nobody has to read it. own is the flush's accepted delivery,
// when there is one.
//
// It takes own's in-process ownership (the seen set) and records the session as needing recovery
// (markRecoveryNeeded) BEFORE the flush is answered. The ownership makes the end exactly-once within
// this daemon: a drain that meets the flush's own line while the end runs leaves it for a later pass
// (drain.go processOne), and a copy of the same delivery — the hook's own spooled fallback, or the
// same line accepted twice — finds it owned or complete and starts nothing. Once Stop has begun
// joining the ends, none is started: a durable flush is then Stop's own drain's to replay, or the
// next daemon's.
func (d *daemon) startSessionEnd(ctx context.Context, req ipc.Request, own job, durable bool) <-chan ipc.Response {
	done := make(chan ipc.Response, 1)
	var ownp *job
	if durable {
		if _, acquired := d.ing.seen.begin(own.key); !acquired {
			// Either this delivery's session end already ran in this process, or another handler (a
			// drain replaying the line) is running it right now. There is nothing left to start, and
			// the flush itself is durable.
			done <- ipc.Response{OK: true}
			return done
		}
		ownp = &own
	}

	e := d.ends
	started := e.begin()
	if started || durable {
		// From here until an end finishes it, the flush is acknowledged work not yet done, so the
		// session is on record as needing recovery BEFORE the answer goes out — as it was when the
		// route did the whole end before answering. A daemon that dies before the end has run leaves
		// the marker beside the WAL line its next drain replays, and a caller that has the answer in
		// hand can wait for the marker to clear, which is the end's own completion record. A flush with
		// no durable line and no end is not marked: nothing would ever clear it.
		d.markRecoveryNeeded(resolveEvent(req).SessionID, recoveryStageBegin, d.DrainGaps().PendingBytes)
	}
	if !started {
		if ownp != nil {
			d.ing.seen.finish(own.key, false)
		}
		if d.m != nil {
			d.m.Counter(counterSessionEndRefused).Add(1)
		}
		d.log.Warn("daemon: flush arrived during shutdown; its session end is left to the next drain",
			"session", string(req.Session), "durable", durable)
		done <- ipc.Response{OK: true}
		return done
	}

	// The request's values (the Services/Registry/Daemon context dispatchOp bound), none of its
	// cancellation: the end outlives the request, and Stop, not the connection, ends it.
	run, cancel := context.WithCancel(context.WithoutCancel(ctx))
	stopAfter := context.AfterFunc(e.ctx, cancel)
	go func() {
		defer e.end()
		defer cancel()
		defer stopAfter()
		resp := ipc.Response{OK: false, Err: "daemon: session end panicked"}
		defer func() { done <- resp }()
		defer func() {
			if r := recover(); r != nil {
				if d.m != nil {
					d.m.Counter(counterHandlerPanic).Add(1)
				}
				d.log.Loud("daemon: session end panicked; its flush stays in the WAL for the next drain",
					"session", string(req.Session), "recover", r)
			}
		}()
		resp = d.endSession(run, req, true, ownp)
	}()
	return done
}

// awaitSessionEnds waits until no session end is running, and reports whether it got there before
// ctx ended. A flush's end is counted before its route answers, so a caller that has the answer in
// hand cannot miss it.
func (d *daemon) awaitSessionEnds(ctx context.Context) bool {
	return d.ends.wait(ctx)
}

// stopSessionEnds is Stop's join for every session end startSessionEnd launched. It closes the gate
// first, so nothing joins the group behind the wait. The ends in flight then get stopDrainBound of
// their own to finish on their merits: each one's work is a flush the host has already been told is
// done, and a clean shutdown should not leave it to the next start. Only then is whatever remains
// cancelled, which every step of an end answers promptly; one that ignores even that is abandoned with
// a Loud line after sessionEndAbandonAfter rather than wedging the shutdown. Either way its flush is
// still unacknowledged in the WAL, and Stop's own drain, which runs next, or the next daemon's startup
// drain replays it.
func (d *daemon) stopSessionEnds(ctx context.Context) {
	e := d.ends
	e.close()
	defer e.cancel()

	grace, cancel := context.WithTimeout(ctx, stopDrainBound)
	defer cancel()
	if d.awaitSessionEnds(grace) {
		return
	}
	e.cancel()
	abandon, stop := context.WithTimeout(context.Background(), sessionEndAbandonAfter)
	defer stop()
	if !d.awaitSessionEnds(abandon) {
		d.log.Loud("daemon: stop: a session end ignored cancellation; abandoning it, its flush stays in the WAL",
			"bound", sessionEndAbandonAfter.String())
	}
}

// finishOwnFlush records that the flush the session end was started for is done, once SessionEnd, the
// marker and the sketches are: a leased flush reaches the committed frontier, and the in-process
// ownership is released as complete, so the end's final drain — and any later one — absorbs the
// flush's own line (and any spooled copy of it) instead of replaying it. A frontier write that fails
// leaves the ownership released as NOT complete: the flush stays pending, and the next drain replays it,
// ending the session again rather than leaving an arrival no successor can ever pass (the ordering gate
// waits for every earlier leased arrival, the flush included).
func (d *daemon) finishOwnFlush(ctx context.Context, own *job, release func(done bool)) {
	if !own.leased {
		release(true)
		return
	}
	if err := d.ing.commitDelivery(ctx, *own, core.Hash{}); err != nil {
		if d.m != nil {
			d.m.Counter(counterDeliveryAckFailed).Add(1)
		}
		d.log.Warn("daemon: flush not acknowledged; its line stays in the WAL for the next drain",
			"session", string(own.req.Session), "err", err)
		release(false)
		return
	}
	release(true)
}

// replayedDeliveryKey carries the lease of a line a drain is replaying to the handler it dispatches
// to, beside the observation identity (observer.WithObservation). A replayed flush reads it to settle
// only the arrivals before its own (sessionEndArrival).
type replayedDeliveryKey struct{}

func withReplayedDelivery(ctx context.Context, lease deliveryLease) context.Context {
	return context.WithValue(ctx, replayedDeliveryKey{}, lease)
}

func replayedDelivery(ctx context.Context) (deliveryLease, bool) {
	l, ok := ctx.Value(replayedDeliveryKey{}).(deliveryLease)
	return l, ok
}

// sessionEndArrival is the flush's own arrival in its session, when it has one: the session end then
// waits only for the arrivals before it. 0 means the flush holds no lease — an unrecorded mode, a
// request with no nonce, or no journal — and the end waits for every leased arrival of the session.
func sessionEndArrival(ctx context.Context, own *job) uint64 {
	if own != nil {
		if own.leased {
			return own.lease.ArrivalSeq
		}
		return 0
	}
	if l, ok := replayedDelivery(ctx); ok {
		return l.ArrivalSeq
	}
	return 0
}

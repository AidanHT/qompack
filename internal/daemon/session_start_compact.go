package daemon

import (
	"context"
	"fmt"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/observer"
)

// The SessionStart(source=compact) answer (C1.16).
//
// A compact SessionStart is the one hook whose answer carries the conversation's memory back into
// the model, and the host gives it ten seconds (internal/cli sessionStartReplyDeadline) before the
// hook client gives up and answers {}. In the packaging lane's live session 2 one took 10.3 s under
// load and the rehydration was lost without a word.
//
// Measured with the load rig (internal/cli TestSessionStartCompact_LoadRig, run with an fsync
// co-load; plans/sdd/V6-closeout/w2-lifetime/runs), the route spent its time in three places, none
// of them building the rehydration (rehydrate.build p99 13 ms):
//
//   - waiting for the observer's per-session lock (observer/session.go takes it before the source
//     switch), which a worker processing one of the SAME session's tool results or subagent stops
//     holds across every store write it makes: p50 229 ms, p99 918 ms;
//   - phase 1's durable writes (state.bin, the contract history, the observation ledger), which the
//     rehydration used to wait behind: p50 295 ms, p99 655 ms;
//   - the rehydration's own drop-report write, made before the answer: p50 66 ms, p99 393 ms.
//
// So the route no longer runs the rehydration inside the observer's SessionStart. It starts the
// rehydration itself, through Services.Rehydrate (the seam SP-05 provisioned for this branch), as
// soon as phase 1's contract run has said the mode may act — before phase 1's writes, so the two
// overlap — and it runs the observer's SessionStart bookkeeping beside it, marked so that the
// observer's compact branch does not build a second rehydration. The route waits for the
// rehydration alone: the observer's bookkeeping (frontier adoption, the segment it ensures) finishes
// whenever the session's lock frees, on a goroutine Stop joins. The rehydration hands its answer
// over BEFORE it writes its drop report, so that write is off the answer's path too.
//
// And the wait is bounded. A rehydration that is not ready within compactAnswerBudget of the
// request's arrival is answered with an explicit deferred note — never {} — that tells the model
// the rehydration did not arrive and how to recover it through the qompack MCP tools. The work
// goes on: a rehydration that finishes later records its drop report as undelivered, so dropped()
// says so rather than describing a payload the model never saw.

// compactAnswerBudget is how long, measured from the request's arrival at the route, the session.start
// route waits for a compact rehydration before it answers with the deferred note instead.
//
// It is one third of the SessionStart hook's manifest timeout (15 s, so 5 s). The hook client's own
// reply deadline is two thirds of that timeout (10 s), and it covers more than this route: the
// client's process start, the daemon start session-start may perform (EnsureRunning polls for up to
// 1.5 s after a spawn), admission, the dial, and the reply's transit back. Answering by half of the
// client's deadline leaves the other half for all of that, so under load the host receives the
// daemon's answer — a rehydration or the note — rather than the client's {}. It is a bound on a
// wait, not a latency budget: the target is the rig's distribution, well inside it.
func compactAnswerBudget() time.Duration {
	const fraction = 3
	ms := manifestHookTimeoutMs(hookEventNameSessionStart)
	if ms <= 0 {
		return defaultCompactAnswerBudget
	}
	return time.Duration(ms/fraction) * time.Millisecond
}

// defaultCompactAnswerBudget is compactAnswerBudget when the manifest names no SessionStart timeout,
// which only a malformed build can produce: one third of the 15 s the manifest has always carried.
const defaultCompactAnswerBudget = 5 * time.Second

// counterCompactDeferred counts compact SessionStarts answered with the deferred note because the
// rehydration was not ready in time (or could not be started, the daemon stopping).
const counterCompactDeferred = "session_start_compact_deferred"

// histSessionStartCompactWait is the route's wait for the compact rehydration, beside the other
// session_start.* phases (handlers.go).
const histSessionStartCompactWait = "session_start.compact_wait"

// The deferred note's reasons: why the rehydration did not reach this answer.
const (
	// DeferredNotReady is the daemon's own: the rehydration was still being built when the answer
	// was due.
	DeferredNotReady = "it was not ready when the answer was due"
	// DeferredStopping is the daemon's own too: the daemon was shutting down.
	DeferredStopping = "the Qompack daemon was shutting down"
	// DeferredFailed is the daemon's own: building it failed outright (a recovered panic).
	DeferredFailed = "building it failed"
	// DeferredNoAnswer is the hook client's: the daemon did not answer the SessionStart hook at all.
	DeferredNoAnswer = "the Qompack daemon did not answer in time"
)

// DeferredNoteTag opens every deferred note, so a transcript, a test or an operator can tell one
// apart from a rehydration (which opens with checkpoint.InjectionOpenTag) at a glance.
const DeferredNoteTag = "<!-- qompack:rehydration-deferred -->"

// deferredNoteMaxSessionRunes bounds the session id the note quotes in its expand() pointer. The id
// is the host's and in practice a UUID; one longer than this is left out rather than truncated,
// because a truncated id is a pointer to nothing.
const deferredNoteMaxSessionRunes = 128

// CompactDeferredNote renders the additionalContext a compact SessionStart carries when its
// rehydration cannot be delivered: an explicit statement that it did not arrive, why, and how to
// recover what it would have carried through the qompack MCP tools. It is a few hundred characters,
// far inside the host's 10,000-character cap (TestCompactDeferredNote_FitsTheHostCap), and it is the
// same text whether the daemon or the hook client writes it.
func CompactDeferredNote(sess core.SessionID, reason string) string {
	expand := ""
	if n := utf8.RuneCountInString(string(sess)); n > 0 && n <= deferredNoteMaxSessionRunes {
		expand = fmt.Sprintf("expand(tool_use_id=%s) returns this session's first request verbatim, ",
			observer.VerbatimPromptID(sess, 0))
	}
	return DeferredNoteTag + "\n" +
		"Qompack could not deliver this compaction's rehydration (" + reason + "), so none of it is in " +
		"this context. What Qompack captured before the compaction can still be retrieved with its MCP " +
		"tools: " + expand + "recall(query) searches the captured tool results and prompts, and dropped() " +
		"lists what the last rehydration left out. Checkpoints are kept in .qompack/checkpoints/ (the " +
		"highest-numbered file is the newest)."
}

// compactDeferredOutput is CompactDeferredNote as a SessionStart hook output.
func compactDeferredOutput(sess core.SessionID, reason string) hookio.Output {
	return hookio.SessionStartOutput(CompactDeferredNote(sess, reason))
}

// compactTicket is the hand-over between the route waiting for a compact rehydration and the
// goroutine building it. Exactly one of offer and abandon wins: either the answer is delivered to
// the route, or the route has already answered with the deferred note and the rehydration learns
// that what it built was never seen.
type compactTicket struct {
	mu    sync.Mutex
	state compactTicketState
	out   hookio.Output
	// failed, when non-empty, is why the work produced no answer at all (fail): the route answers
	// with the deferred note for that reason instead of out.
	failed string
	ready  chan struct{}
}

type compactTicketState int

const (
	ticketPending compactTicketState = iota
	ticketDelivered
	ticketAbandoned
)

func newCompactTicket() *compactTicket { return &compactTicket{ready: make(chan struct{})} }

// offer hands out to the route and reports whether the route will use it. The first offer wins; a
// later one (the work goroutine's own, after the rehydrator already offered) changes nothing and
// reports the same outcome.
func (t *compactTicket) offer(out hookio.Output) bool { return t.settle(out, "") }

// fail is offer for work that produced no answer at all: the route answers with the deferred note,
// naming reason, rather than with an empty output that would say nothing.
func (t *compactTicket) fail(reason string) { t.settle(hookio.Empty(), reason) }

func (t *compactTicket) settle(out hookio.Output, failed string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	switch t.state {
	case ticketPending:
		t.out, t.failed, t.state = out, failed, ticketDelivered
		close(t.ready)
		return true
	case ticketDelivered:
		return true
	default:
		return false
	}
}

// abandon records that the route answered without this rehydration, and reports false if an offer
// won first — in which case result holds the answer and the route uses it after all.
func (t *compactTicket) abandon() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.state != ticketPending {
		return false
	}
	t.state = ticketAbandoned
	return true
}

// result is the offered answer, and the failure reason when the work failed instead. It is only
// meaningful once ready is closed.
func (t *compactTicket) result() (hookio.Output, string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.out, t.failed
}

// compactTicketKey carries a compactTicket to the rehydration service (rehydrate_service.go).
type compactTicketKey struct{}

func withCompactTicket(ctx context.Context, t *compactTicket) context.Context {
	return context.WithValue(ctx, compactTicketKey{}, t)
}

func compactTicketFrom(ctx context.Context) *compactTicket {
	t, _ := ctx.Value(compactTicketKey{}).(*compactTicket)
	return t
}

// routeRehydratesKey marks the observer's SessionStart call the route makes beside its own
// rehydration: the rehydration service answers such a call's compact branch with the empty output
// instead of building a second rehydration nobody would read.
type routeRehydratesKey struct{}

func withRouteRehydrates(ctx context.Context) context.Context {
	return context.WithValue(ctx, routeRehydratesKey{}, true)
}

func routeRehydrates(ctx context.Context) bool {
	v, _ := ctx.Value(routeRehydratesKey{}).(bool)
	return v
}

// compactAnswer is one compact SessionStart's pending answer: the ticket its rehydration offers
// through, and the instant the route must answer by.
type compactAnswer struct {
	sess    core.SessionID
	ticket  *compactTicket
	due     time.Time
	started bool
}

// startCompactAnswer starts a compact SessionStart's work and returns the answer to wait for. It is
// called only once the route's contract run has said the mode may act.
//
// With the daemon's own rehydrator bound (Services.Rehydrate, which production always binds), the
// rehydration runs on one goroutine and the observer's SessionStart bookkeeping, marked
// routeRehydrates, on another; the route waits for the first only. Without one, the SessionStart
// seam is the whole of the work, as it always was, and the route waits for it under the same bound.
//
// Both goroutines run on the daemon's reply-work lifetime (startReplyWork): they keep the request's
// values, lose its cancellation, and Stop joins them.
func (d *daemon) startCompactAnswer(ctx context.Context, ev hookio.Event, arrived time.Time) *compactAnswer {
	a := &compactAnswer{sess: ev.SessionID, ticket: newCompactTicket(), due: arrived.Add(d.compactBudget)}

	rehydrate, sessionStart := d.svc.Rehydrate, d.svc.SessionStart
	var answer func(context.Context) (hookio.Output, error)
	switch {
	case rehydrate != nil:
		answer = func(c context.Context) (hookio.Output, error) { return rehydrate(c, ev) }
	case sessionStart != nil:
		answer = func(c context.Context) (hookio.Output, error) { return sessionStart(c, ev) }
	default:
		a.ticket.offer(hookio.Empty())
		a.started = true
		return a
	}

	ticket := a.ticket
	a.started = d.startReplyWork(withCompactTicket(ctx, ticket), "the compact rehydration", func(c context.Context) {
		out, err := answer(c)
		if err != nil {
			d.log.Warn("daemon: SessionStart failed", "err", err)
			out = hookio.Empty()
		}
		ticket.offer(out)
	}, func() { ticket.fail(DeferredFailed) })

	if rehydrate != nil && sessionStart != nil {
		// The bookkeeping half. Its output is the empty output by construction (routeRehydrates),
		// so only a failure is worth reporting.
		d.startReplyWork(withRouteRehydrates(ctx), "the compact SessionStart bookkeeping", func(c context.Context) {
			if _, err := sessionStart(c, ev); err != nil {
				d.log.Warn("daemon: SessionStart failed", "err", err)
			}
		}, nil)
	}
	return a
}

// awaitCompactAnswer waits for a's rehydration until a.due and returns what the route answers with:
// the rehydration, or — if it is not ready, or could not be started because the daemon is stopping
// — the deferred note. It never returns the empty output for a rehydration that simply ran late.
//
// ctx ending first (Stop cancels the serving context; a drain's bounded context expires) ends the
// wait the same way: the answer is due now.
func (d *daemon) awaitCompactAnswer(ctx context.Context, a *compactAnswer) hookio.Output {
	start := time.Now()
	defer d.observePhase(histSessionStartCompactWait, start)

	if !a.started {
		return d.deferCompactAnswer(a, DeferredStopping)
	}
	t := time.NewTimer(time.Until(a.due))
	defer t.Stop()
	select {
	case <-a.ticket.ready:
	case <-t.C:
		if a.ticket.abandon() {
			return d.deferCompactAnswer(a, DeferredNotReady)
		}
		// An offer won the race with the timer: the answer is here after all.
	case <-ctx.Done():
		if a.ticket.abandon() {
			return d.deferCompactAnswer(a, DeferredNotReady)
		}
	}
	out, failed := a.ticket.result()
	if failed != "" {
		return d.deferCompactAnswer(a, failed)
	}
	return out
}

// deferCompactAnswer counts, reports and renders one deferred answer. It is Loud: an answer that
// carries no rehydration is a degradation of acting (§13 invariant 10), and the only other trace of
// it would be a note in a transcript the operator may never read.
func (d *daemon) deferCompactAnswer(a *compactAnswer, reason string) hookio.Output {
	if d.m != nil {
		d.m.Counter(counterCompactDeferred).Add(1)
	}
	d.log.Loud("daemon: compact SessionStart answered without its rehydration",
		"session", string(a.sess), "reason", reason, "budget", d.compactBudget.String())
	return compactDeferredOutput(a.sess, reason)
}

// startReplyWork runs work on a goroutine that outlives the request that started it, under the same
// lifetime the verbatim prompt captures use (startPromptRecording): a context that keeps ctx's
// values and none of its cancellation, cancelled only by Stop, and joined by Stop's
// stopPromptRecordings through the same WaitGroup — so Stop never closes the store under a
// SessionStart's rehydration or bookkeeping either.
//
// It reports false, and runs nothing, once Stop has begun that join. A panic inside work is
// recovered, counted and Loud'd, and then panicked (when non-nil) is called, so a waiter is answered
// at once rather than left to run out its bound: this goroutine outlives the request, so
// callHandler's recovery can no longer reach it, and one broken seam must never bring the daemon
// down.
func (d *daemon) startReplyWork(ctx context.Context, what string, work func(context.Context), panicked func()) bool {
	d.promptMu.Lock()
	if d.promptClosed {
		d.promptMu.Unlock()
		d.log.Warn("daemon: request work arrived during shutdown; not started", "work", what)
		return false
	}
	d.promptWG.Add(1)
	d.promptMu.Unlock()

	c, cancel := context.WithCancel(context.WithoutCancel(ctx))
	stopAfter := context.AfterFunc(d.promptCtx, cancel)
	go func() {
		defer d.promptWG.Done()
		defer cancel()
		defer stopAfter()
		defer func() {
			if r := recover(); r != nil {
				if d.m != nil {
					d.m.Counter(counterHandlerPanic).Add(1)
				}
				d.log.Loud("daemon: request work panicked", "work", what, "recover", r)
				if panicked != nil {
					panicked()
				}
			}
		}()
		work(c)
	}()
	return true
}

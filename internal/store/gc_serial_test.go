package store

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
)

// GC passes on one store run one at a time (V6 close-out w6-gcserial). Every concurrent session end
// runs a pass (C1.15) and so does the idle scheduler, and two passes that overlap race on gc.json's
// resume cursor, gc-live.bin, the tombstone phase and retention-roots.jsonl's compaction.

// gcSerialBound bounds every wait in this file. It is a failure bound only: each wait ends on an
// event, and the bound is reached only when that event never comes.
const gcSerialBound = 30 * time.Second

// gcSerialTick is how often a wait in this file re-reads what it waits for.
const gcSerialTick = time.Millisecond

// gcOverlapProbe instruments the package's after-harvest hook, which every GC pass reaches once,
// after its harvest and before its tombstones, sweep, cursor and compaction. It records how many
// passes were inside the hook at once, and holds the FIRST pass there until release is opened, so a
// test can start other passes while one is provably in the middle of its work.
type gcOverlapProbe struct {
	inside, most, entries atomic.Int32
	first                 chan struct{}
	release               chan struct{}
	once                  sync.Once
}

// installGCOverlapProbe installs the probe for the rest of the test. The hook is package-global, so
// a test using it must not run in parallel with another test that runs GC.
func installGCOverlapProbe(t *testing.T) *gcOverlapProbe {
	t.Helper()
	p := &gcOverlapProbe{first: make(chan struct{}), release: make(chan struct{})}
	hook := func() {
		n := p.inside.Add(1)
		for {
			m := p.most.Load()
			if n <= m || p.most.CompareAndSwap(m, n) {
				break
			}
		}
		if p.entries.Add(1) == 1 {
			close(p.first)
			<-p.release
		}
		p.inside.Add(-1)
	}
	prev := gcAfterHarvest.Swap(&hook)
	t.Cleanup(func() {
		p.open()
		gcAfterHarvest.Store(prev)
	})
	return p
}

// open releases the held pass. It is idempotent.
func (p *gcOverlapProbe) open() { p.once.Do(func() { close(p.release) }) }

// awaitFirst waits for the first pass to be held inside the hook.
func (p *gcOverlapProbe) awaitFirst(t *testing.T) {
	t.Helper()
	select {
	case <-p.first:
	case <-time.After(gcSerialBound):
		require.FailNow(t, "the first GC pass never reached its after-harvest hook")
	}
}

// gcResult is one GC call's return.
type gcResult struct {
	rep GCReport
	err error
}

// gcAsync runs one GC call on its own goroutine and returns the channel its result arrives on.
func gcAsync(ctx context.Context, s *FSStore, p GCPolicy) <-chan gcResult {
	out := make(chan gcResult, 1)
	go func() {
		rep, err := s.GC(ctx, p)
		out <- gcResult{rep: rep, err: err}
	}()
	return out
}

// awaitGC waits for one asynchronous GC call's result.
func awaitGC(t *testing.T, ch <-chan gcResult) gcResult {
	t.Helper()
	select {
	case r := <-ch:
		return r
	case <-time.After(gcSerialBound):
		require.FailNow(t, "a GC call never returned")
		return gcResult{}
	}
}

// TestGC_PassesStartedTogetherRunOneAtATime starts several passes on one store while the first is
// held in the middle of its work. None of the others may reach that point until the first has
// ended: they wait behind it, and every request that arrived while it ran is answered by exactly one
// follow-up pass that starts after it. Before the fix every pass ran at once (runs/01).
func TestGC_PassesStartedTogetherRunOneAtATime(t *testing.T) {
	// Not parallel: the after-harvest hook is package-global.
	const callers = 4
	tp := newTestStore(t)
	for i := 0; i < 6; i++ {
		gcSeed(t, tp, fmt.Sprintf("src/serial%02d.ts", i), fmt.Sprintf("serial body %d, unique\n", i))
	}
	probe := installGCOverlapProbe(t)
	ctx := context.Background()

	results := []<-chan gcResult{gcAsync(ctx, tp.Store, forceCollect)}
	probe.awaitFirst(t)
	for i := 1; i < callers; i++ {
		results = append(results, gcAsync(ctx, tp.Store, forceCollect))
	}

	// Either another pass reaches the hook while the first is held (they overlap), or every later
	// request is counted as waiting behind the running pass. Nothing else can end this wait.
	queued := tp.Metrics.Counter(CounterGCQueued)
	require.Eventually(t, func() bool {
		return probe.most.Load() > 1 || queued.Value() == callers-1
	}, gcSerialBound, gcSerialTick, "no later GC request either overlapped the held pass or queued behind it")
	require.EqualValues(t, 1, probe.most.Load(),
		"two GC passes were in the middle of their work at once on one store")

	probe.open()
	var reps []GCReport
	for _, ch := range results {
		r := awaitGC(t, ch)
		require.NoError(t, r.err)
		reps = append(reps, r.rep)
	}
	require.EqualValues(t, 1, probe.most.Load(), "passes never overlapped, before or after the release")
	require.EqualValues(t, 2, probe.entries.Load(),
		"the held pass, then exactly one follow-up for the %d requests that queued behind it", callers-1)
	require.EqualValues(t, callers-2, tp.Metrics.Counter(CounterGCShared).Value(),
		"one queued request ran the follow-up and it answered the other %d", callers-2)
	require.Positive(t, reps[0].DeletedObjects, "the first pass collected the unreferenced seeds")
	for i := 1; i < callers; i++ {
		require.Zero(t, reps[i].DeletedObjects,
			"the follow-up found nothing left to collect: the held pass's work was not repeated or recounted")
	}
}

// TestGC_ARequestMadeDuringAPassIsAnsweredByOneThatStartsAfterIt: a pass that is already running
// harvested its retention sources before a later request existed, so it cannot answer that request.
// Here a checkpoint holds a root while the first pass harvests; the checkpoint is then removed and a
// second request made. The first pass keeps the root, as it must (it read the checkpoint), and the
// second request is answered by a follow-up that does not see the checkpoint and collects the root.
// A gate that let the second request join the running pass would hand it the first pass's answer and
// leave the root until some later pass happened to run.
func TestGC_ARequestMadeDuringAPassIsAnsweredByOneThatStartsAfterIt(t *testing.T) {
	// Not parallel: the after-harvest hook is package-global.
	tp := newTestStore(t)
	held := gcSeed(t, tp, "src/held.ts", "held by a checkpoint only while the first pass harvests\n")
	writeCheckpointJSON(t, tp, "0001.json", held.Hash.String())
	probe := installGCOverlapProbe(t)
	ctx := context.Background()

	first := gcAsync(ctx, tp.Store, forceCollect)
	probe.awaitFirst(t)
	require.NoError(t, os.Remove(paths.Long(filepath.Join(paths.Of(tp.Root).Checkpoints, "0001.json"))))
	second := gcAsync(ctx, tp.Store, forceCollect)
	require.Eventually(t, func() bool { return tp.Metrics.Counter(CounterGCQueued).Value() == 1 },
		gcSerialBound, gcSerialTick, "the second request never queued behind the running pass")

	probe.open()
	r1 := awaitGC(t, first)
	require.NoError(t, r1.err)
	require.Zero(t, r1.rep.DeletedObjects, "the running pass read the checkpoint, so it keeps the root")
	r2 := awaitGC(t, second)
	require.NoError(t, r2.err)
	require.Positive(t, r2.rep.DeletedObjects,
		"the second request is answered by a pass that started after it, which no longer sees the checkpoint")
	_, err := tp.Store.GetRoot(ctx, held.Hash)
	require.ErrorIs(t, err, core.ErrNotFound, "the root the removed checkpoint held is collected")
	require.EqualValues(t, 2, probe.entries.Load(), "two passes ran, one after the other")
	require.EqualValues(t, 1, probe.most.Load(), "and they never overlapped")
}

// ── the gate, over a scripted pass ─────────────────────────────────────────────────────────────

// scriptedPass is a gcPassFunc a test drives: each call announces itself on started and runs until
// the test finishes it.
type scriptedPass struct {
	started chan *scriptedRun
	calls   atomic.Int32
	inside  atomic.Int32
	most    atomic.Int32
}

// scriptedRun is one call of a scriptedPass.
type scriptedRun struct {
	ctx context.Context
	p   GCPolicy
	end chan scriptedEnd
}

// scriptedEnd is how the test ends one run: with a report and an error, or with a panic.
type scriptedEnd struct {
	rep   GCReport
	err   error
	panic bool
}

func newScriptedPass() *scriptedPass {
	return &scriptedPass{started: make(chan *scriptedRun, 16)}
}

func (sp *scriptedPass) pass(ctx context.Context, p GCPolicy) (GCReport, error) {
	sp.calls.Add(1)
	n := sp.inside.Add(1)
	defer sp.inside.Add(-1)
	for {
		m := sp.most.Load()
		if n <= m || sp.most.CompareAndSwap(m, n) {
			break
		}
	}
	run := &scriptedRun{ctx: ctx, p: p, end: make(chan scriptedEnd, 1)}
	sp.started <- run
	e := <-run.end
	if e.panic {
		panic("scripted gc pass panicked")
	}
	return e.rep, e.err
}

// next waits for the next run to start.
func (sp *scriptedPass) next(t *testing.T) *scriptedRun {
	t.Helper()
	select {
	case r := <-sp.started:
		return r
	case <-time.After(gcSerialBound):
		require.FailNow(t, "no scripted GC pass started")
		return nil
	}
}

// noneStarted asserts that no run is waiting to be taken.
func (sp *scriptedPass) noneStarted(t *testing.T) {
	t.Helper()
	select {
	case r := <-sp.started:
		require.FailNow(t, "a scripted GC pass started that nothing should have started", "%+v", r.p)
	default:
	}
}

// gateCall runs one serializeGC call on its own goroutine. A panic in it is caught and delivered as
// the result's err, so a test can inject one without killing the test binary.
func gateCall(ctx context.Context, s *FSStore, p GCPolicy, sp *scriptedPass) <-chan gcResult {
	out := make(chan gcResult, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				out <- gcResult{err: fmt.Errorf("panic: %v", r)}
			}
		}()
		rep, err := s.serializeGC(ctx, p, sp.pass)
		out <- gcResult{rep: rep, err: err}
	}()
	return out
}

// awaitQueued waits until n requests in all have queued behind a running pass.
func awaitQueued(t *testing.T, tp *testProject, n int64) {
	t.Helper()
	require.Eventually(t, func() bool { return tp.Metrics.Counter(CounterGCQueued).Value() == n },
		gcSerialBound, gcSerialTick, "expected %d queued GC requests", n)
}

// gateStartHeld starts one call that runs a pass at once, and returns it with its run.
func gateStartHeld(t *testing.T, tp *testProject, sp *scriptedPass, p GCPolicy) (<-chan gcResult, *scriptedRun) {
	t.Helper()
	ch := gateCall(context.Background(), tp.Store, p, sp)
	return ch, sp.next(t)
}

// TestGCGate_AnUncontendedCallRunsItsOwnPassOnItsOwnContext: with nothing running, a call runs its
// pass at once, with the caller's own context and policy, and returns that pass's report unchanged.
func TestGCGate_AnUncontendedCallRunsItsOwnPassOnItsOwnContext(t *testing.T) {
	tp := newTestStore(t)
	sp := newScriptedPass()
	type ctxKey struct{}
	ctx := context.WithValue(context.Background(), ctxKey{}, "mine")
	p := GCPolicy{RetainDays: 3, Deadline: time.Second}

	ch := gateCall(ctx, tp.Store, p, sp)
	run := sp.next(t)
	require.Equal(t, "mine", run.ctx.Value(ctxKey{}), "the pass runs on the caller's own context")
	require.Equal(t, p, run.p, "and with the caller's own policy")
	run.end <- scriptedEnd{rep: GCReport{DeletedObjects: 3, Truncated: true}}
	r := awaitGC(t, ch)
	require.NoError(t, r.err)
	require.Equal(t, GCReport{DeletedObjects: 3, Truncated: true}, r.rep)
	require.Zero(t, tp.Metrics.Counter(CounterGCQueued).Value(), "nothing waited")
	require.EqualValues(t, 1, sp.calls.Load())
}

// TestGCGate_RequestsWaitingBehindAPassShareOneFollowUp: requests that arrive while a pass runs are
// answered together by one follow-up pass that starts after it, never by the pass they found running.
func TestGCGate_RequestsWaitingBehindAPassShareOneFollowUp(t *testing.T) {
	tp := newTestStore(t)
	sp := newScriptedPass()
	first, running := gateStartHeld(t, tp, sp, GCPolicy{})

	var waiting []<-chan gcResult
	for i := 0; i < 3; i++ {
		waiting = append(waiting, gateCall(context.Background(), tp.Store, GCPolicy{}, sp))
	}
	awaitQueued(t, tp, 3)
	sp.noneStarted(t)

	running.end <- scriptedEnd{rep: GCReport{DeletedObjects: 1}}
	require.Equal(t, 1, awaitGC(t, first).rep.DeletedObjects)
	followUp := sp.next(t)
	sp.noneStarted(t)
	followUp.end <- scriptedEnd{rep: GCReport{DeletedObjects: 7, Outcomes: []RootOutcome{{Reason: "shared"}}}}
	var reps []GCReport
	for _, ch := range waiting {
		r := awaitGC(t, ch)
		require.NoError(t, r.err)
		require.Equal(t, 7, r.rep.DeletedObjects, "answered by the follow-up, not by the pass they found running")
		reps = append(reps, r.rep)
	}
	reps[0].Outcomes[0].Reason = "changed by one caller"
	require.Equal(t, "shared", reps[1].Outcomes[0].Reason, "no two callers share an Outcomes slice")
	require.EqualValues(t, 2, sp.calls.Load(), "the running pass and one follow-up")
	require.EqualValues(t, 1, sp.most.Load(), "never two at once")
	require.EqualValues(t, 2, tp.Metrics.Counter(CounterGCShared).Value())
}

// TestGCGate_AWaitingRequestWithdrawsWhenItsContextEnds: the wait answers to the caller's context,
// and a request nobody waits for any more starts no pass.
func TestGCGate_AWaitingRequestWithdrawsWhenItsContextEnds(t *testing.T) {
	tp := newTestStore(t)
	sp := newScriptedPass()
	first, running := gateStartHeld(t, tp, sp, GCPolicy{})

	ctx, cancel := context.WithCancel(context.Background())
	waiting := gateCall(ctx, tp.Store, GCPolicy{}, sp)
	awaitQueued(t, tp, 1)
	cancel()
	r := awaitGC(t, waiting)
	require.ErrorIs(t, r.err, context.Canceled, "the caller gave up while the other pass still runs")

	running.end <- scriptedEnd{}
	require.NoError(t, awaitGC(t, first).err)
	sp.noneStarted(t)
	require.EqualValues(t, 1, sp.calls.Load(), "no follow-up runs for a withdrawn request")

	again := gateCall(context.Background(), tp.Store, GCPolicy{}, sp)
	sp.next(t).end <- scriptedEnd{rep: GCReport{DeletedObjects: 2}}
	require.Equal(t, 2, awaitGC(t, again).rep.DeletedObjects, "the gate is free again")
}

// TestGCGate_AFollowUpItsStarterAbandonedDoesNotAnswerTheOthers: the follow-up runs on its starter's
// context. When that caller gives up mid-pass, the pass ends with the caller's cancellation, which
// answers nobody else: the next waiting request runs a pass of its own.
func TestGCGate_AFollowUpItsStarterAbandonedDoesNotAnswerTheOthers(t *testing.T) {
	tp := newTestStore(t)
	sp := newScriptedPass()
	first, running := gateStartHeld(t, tp, sp, GCPolicy{})

	starterCtx, cancelStarter := context.WithCancel(context.Background())
	defer cancelStarter()
	starter := gateCall(starterCtx, tp.Store, GCPolicy{}, sp)
	awaitQueued(t, tp, 1)
	other := gateCall(context.Background(), tp.Store, GCPolicy{}, sp)
	awaitQueued(t, tp, 2)

	running.end <- scriptedEnd{}
	require.NoError(t, awaitGC(t, first).err)
	followUp := sp.next(t)
	require.True(t, followUp.ctx == starterCtx, "the oldest request starts the follow-up on its own context")
	cancelStarter()
	followUp.end <- scriptedEnd{err: context.Canceled}
	require.ErrorIs(t, awaitGC(t, starter).err, context.Canceled)

	own := sp.next(t)
	require.False(t, own.ctx == starterCtx, "the other request runs its own pass")
	own.end <- scriptedEnd{rep: GCReport{DeletedObjects: 5}}
	r := awaitGC(t, other)
	require.NoError(t, r.err)
	require.Equal(t, 5, r.rep.DeletedObjects)
	require.EqualValues(t, 3, sp.calls.Load())
}

// TestGCGate_TheLongestDeadlineOfAKindStartsTheFollowUp: of the requests that share a kind of pass,
// the one granting the most time starts it, so the others, whose Deadlines it covers, are all
// answered by it even when it is truncated.
func TestGCGate_TheLongestDeadlineOfAKindStartsTheFollowUp(t *testing.T) {
	tp := newTestStore(t)
	sp := newScriptedPass()
	first, running := gateStartHeld(t, tp, sp, GCPolicy{})

	var waiting []<-chan gcResult
	for i, d := range []time.Duration{2 * time.Second, 8 * time.Second, 5 * time.Second} {
		waiting = append(waiting, gateCall(context.Background(), tp.Store, GCPolicy{Deadline: d}, sp))
		awaitQueued(t, tp, int64(i+1))
	}
	running.end <- scriptedEnd{}
	require.NoError(t, awaitGC(t, first).err)
	followUp := sp.next(t)
	require.Equal(t, 8*time.Second, followUp.p.Deadline, "the longest Deadline starts the follow-up")
	followUp.end <- scriptedEnd{rep: GCReport{DeletedObjects: 4, Truncated: true}}
	for _, ch := range waiting {
		r := awaitGC(t, ch)
		require.NoError(t, r.err)
		require.Equal(t, GCReport{DeletedObjects: 4, Truncated: true}, r.rep)
	}
	require.EqualValues(t, 2, sp.calls.Load())
}

// TestGCGate_ADifferentKindOfRequestWaitsForItsOwnPass: a dry run cannot answer a collecting request
// or the reverse. The oldest waiting request's kind runs first, answering every waiter of that kind,
// and the other kind gets its own pass next.
func TestGCGate_ADifferentKindOfRequestWaitsForItsOwnPass(t *testing.T) {
	tp := newTestStore(t)
	sp := newScriptedPass()
	first, running := gateStartHeld(t, tp, sp, GCPolicy{})

	dry1 := gateCall(context.Background(), tp.Store, GCPolicy{DryRun: true}, sp)
	awaitQueued(t, tp, 1)
	collecting := gateCall(context.Background(), tp.Store, GCPolicy{}, sp)
	awaitQueued(t, tp, 2)
	dry2 := gateCall(context.Background(), tp.Store, GCPolicy{DryRun: true}, sp)
	awaitQueued(t, tp, 3)

	running.end <- scriptedEnd{}
	require.NoError(t, awaitGC(t, first).err)
	dryPass := sp.next(t)
	require.True(t, dryPass.p.DryRun, "the oldest waiting request's kind runs first")
	sp.noneStarted(t)
	dryPass.end <- scriptedEnd{rep: GCReport{ScannedObjects: 9}}
	require.Equal(t, 9, awaitGC(t, dry1).rep.ScannedObjects)
	require.Equal(t, 9, awaitGC(t, dry2).rep.ScannedObjects, "every waiter of that kind is answered by it")

	realPass := sp.next(t)
	require.False(t, realPass.p.DryRun, "the collecting request gets a pass of its own")
	realPass.end <- scriptedEnd{rep: GCReport{DeletedObjects: 6}}
	require.Equal(t, 6, awaitGC(t, collecting).rep.DeletedObjects)
	require.EqualValues(t, 3, sp.calls.Load())
}

// TestGCGate_APanickingPassHandsTheGateOn: a pass that panics still releases the gate, and answers
// nobody: the request waiting behind it runs its own pass.
func TestGCGate_APanickingPassHandsTheGateOn(t *testing.T) {
	tp := newTestStore(t)
	sp := newScriptedPass()
	first, running := gateStartHeld(t, tp, sp, GCPolicy{})
	waiting := gateCall(context.Background(), tp.Store, GCPolicy{}, sp)
	awaitQueued(t, tp, 1)

	running.end <- scriptedEnd{panic: true}
	require.ErrorContains(t, awaitGC(t, first).err, "scripted gc pass panicked", "the panic reaches its own caller")
	own := sp.next(t)
	own.end <- scriptedEnd{rep: GCReport{DeletedObjects: 1}}
	r := awaitGC(t, waiting)
	require.NoError(t, r.err)
	require.Equal(t, 1, r.rep.DeletedObjects, "answered by a pass of its own, not by the one that panicked")
}

// TestGCGate_ServesOnlyWhatThePassRanToItsOwnConclusionFor pins gcOutcome.serves row by row.
func TestGCGate_ServesOnlyWhatThePassRanToItsOwnConclusionFor(t *testing.T) {
	tp := newTestStore(t)
	s := tp.Store
	waiter := func(p GCPolicy) *gcWaiter { return &gcWaiter{p: p, key: s.gcKey(p)} }
	outcome := func(p GCPolicy, rep GCReport, err error) gcOutcome {
		return gcOutcome{p: p, key: s.gcKey(p), rep: rep, err: err}
	}
	deadline := func(d time.Duration) GCPolicy { return GCPolicy{Deadline: d} }
	rows := []struct {
		name  string
		out   gcOutcome
		w     *gcWaiter
		serve bool
	}{
		{"a completed pass of the same kind", outcome(GCPolicy{}, GCReport{}, nil), waiter(GCPolicy{}), true},
		{"a store failure is the waiter's too", outcome(GCPolicy{}, GCReport{}, core.ErrDegraded), waiter(GCPolicy{}), true},
		{"a halted pass read the sources the waiter would", outcome(GCPolicy{}, GCReport{RetentionRootsError: true}, nil), waiter(GCPolicy{}), true},
		{"the starter's cancellation", outcome(GCPolicy{}, GCReport{}, context.Canceled), waiter(GCPolicy{}), false},
		{"the starter's context deadline", outcome(GCPolicy{}, GCReport{}, fmt.Errorf("store: gc sweep: %w", context.DeadlineExceeded)), waiter(GCPolicy{}), false},
		{"a panicked pass", gcOutcome{panicked: true, key: s.gcKey(GCPolicy{})}, waiter(GCPolicy{}), false},
		{"a dry run for a collecting request", outcome(GCPolicy{DryRun: true}, GCReport{}, nil), waiter(GCPolicy{}), false},
		{"a different retention window", outcome(GCPolicy{RetainDays: 5}, GCReport{}, nil), waiter(GCPolicy{}), false},
		{"the configured window spelled as zero or as itself", outcome(GCPolicy{RetainDays: tp.Cfg.Store.Retention.Days}, GCReport{}, nil), waiter(GCPolicy{}), true},
		{"a different quota", outcome(GCPolicy{QuotaBytes: 10}, GCReport{}, nil), waiter(GCPolicy{}), false},
		{"no quota spelled negative", outcome(GCPolicy{QuotaBytes: -1}, GCReport{}, nil), waiter(GCPolicy{}), true},
		{"a different outcome bound", outcome(GCPolicy{MaxOutcomes: -1}, GCReport{}, nil), waiter(GCPolicy{}), false},
		{"a truncated pass that granted as much time", outcome(deadline(time.Second), GCReport{Truncated: true}, nil), waiter(deadline(time.Second)), true},
		{"a truncated pass that granted more time", outcome(deadline(2*time.Second), GCReport{Truncated: true}, nil), waiter(deadline(time.Second)), true},
		{"a truncated pass that granted less time", outcome(deadline(time.Second), GCReport{Truncated: true}, nil), waiter(deadline(2 * time.Second)), false},
		{"a truncated pass for an unbounded waiter", outcome(deadline(time.Second), GCReport{Truncated: true}, nil), waiter(GCPolicy{}), false},
		{"a completed pass that granted less time", outcome(deadline(time.Second), GCReport{}, nil), waiter(GCPolicy{}), true},
	}
	for _, row := range rows {
		require.Equal(t, row.serve, row.out.serves(row.w), row.name)
	}
}

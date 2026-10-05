package daemon

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/paths"
)

// The WAL group-commit tests (design §6.2 T4–T8). They run the production Accept on goroutines of
// their own, the way the IPC server runs one per connection, and watch the WAL through the ingest's
// two seams, writeWAL and syncWAL. Every ordering they rely on is made by holding a Sync open and by
// the queue's own length. The clock only bounds waits that a correct build never reaches, so that
// a deadlock fails the test instead of hanging it.

// walTestLineSize is the length, before its terminator, of every line T4–T7 accept, so that a
// rotation case can put the ceiling between two lines exactly.
const walTestLineSize = 64

// walReq is one request: what Accept is handed, and the line the WAL must hold for it.
type walReq struct {
	req  ipc.Request
	line []byte
	want []byte
}

// newWALReq builds request k on sess, a line of exactly walTestLineSize bytes. Even k hands Accept
// the wire form, terminator included, and odd k the unterminated form, so every test also checks
// that each line gets exactly one terminator whichever form it arrived in.
func newWALReq(t *testing.T, sess core.SessionID, k int) walReq {
	t.Helper()
	head := `{"s":"` + string(sess) + `","k":` + strconv.Itoa(k) + `,"p":"`
	const tail = `"}`
	pad := walTestLineSize - len(head) - len(tail)
	require.GreaterOrEqual(t, pad, 0, "the session name leaves no room in walTestLineSize")
	body := head + strings.Repeat("x", pad) + tail
	line := []byte(body)
	if k%2 == 0 {
		line = append(line, '\n')
	}
	return walReq{req: ipc.Request{Op: ipc.OpObserveTool, Session: sess}, line: line, want: []byte(body + "\n")}
}

// newWALReqs builds one request per session of order, numbered from first.
func newWALReqs(t *testing.T, first int, order ...core.SessionID) []walReq {
	t.Helper()
	reqs := make([]walReq, len(order))
	for k, sess := range order {
		reqs[k] = newWALReq(t, sess, first+k)
	}
	return reqs
}

// wantsOn is what the WAL must hold for the requests of reqs on sess, in order.
func wantsOn(reqs []walReq, sess core.SessionID) string {
	var b strings.Builder
	for _, r := range reqs {
		if r.req.Session == sess {
			b.Write(r.want)
		}
	}
	return b.String()
}

// segName is the base name of sess's WAL segment at rotation seq.
func segName(sess core.SessionID, seq int) string { return filepath.Base(walPath("", sess, seq)) }

// readWAL returns the content of sess's WAL segment at rotation seq under root.
func readWAL(t *testing.T, root string, sess core.SessionID, seq int) string {
	t.Helper()
	b, err := os.ReadFile(paths.Long(walPath(paths.Of(root).Spool, sess, seq)))
	require.NoError(t, err)
	return string(b)
}

// walBytes is the byte count ing keeps for sess's current segment.
func walBytes(ing *ingest, sess core.SessionID) int64 {
	ing.mu.Lock()
	defer ing.mu.Unlock()
	return ing.wals[sess].bytes
}

// walOp is one event in a walProbe's log: a seam call that ended, an Accept that returned or
// panicked, or a test's own note.
type walOp struct {
	at   int      // the event's index in the log
	kind string   // "write", "sync", "return", "panic", or a test's own kind
	call int      // write, sync: the call's 1-based index among the probe's calls of that kind
	seg  string   // write, sync: the segment file's base name
	f    *os.File // write, sync: the handle the call was made on
	data []byte   // write: the bytes the call reported written
	err  error    // write, sync, return: the result
	id   int      // return, panic: the request's id
	what string   // a test's own note
}

// walProbe instruments one ingest's WAL seams. It numbers every Write and every Sync, holds the
// first Sync on gate when there is one, hands each call to the test's hook in place of the real
// call when there is one, and logs how the call ended. The gate and the hooks are set before the
// first Accept and never changed afterwards.
type walProbe struct {
	gate    *walGate
	onWrite func(call int, seg string, f *os.File, b []byte) (int, error)
	onSync  func(call int, seg string, f *os.File) error

	writes, syncs atomic.Int32

	mu  sync.Mutex
	ops []walOp
}

func newWALProbe(ing *ingest) *walProbe {
	p := &walProbe{}
	ing.writeWAL = func(f *os.File, b []byte) (int, error) {
		call, seg := int(p.writes.Add(1)), filepath.Base(f.Name())
		var n int
		var err error
		if p.onWrite != nil {
			n, err = p.onWrite(call, seg, f, b)
		} else {
			n, err = f.Write(b)
		}
		data := slices.Clone(b[:min(max(n, 0), len(b))])
		p.add(walOp{kind: "write", call: call, seg: seg, f: f, data: data, err: err})
		return n, err
	}
	ing.syncWAL = func(f *os.File) error {
		call, seg := int(p.syncs.Add(1)), filepath.Base(f.Name())
		if call == 1 && p.gate != nil {
			p.gate.hold()
		}
		var err error
		if p.onSync != nil {
			err = p.onSync(call, seg, f)
		} else {
			err = f.Sync()
		}
		p.add(walOp{kind: "sync", call: call, seg: seg, f: f, err: err})
		return err
	}
	return p
}

func (p *walProbe) add(op walOp) {
	p.mu.Lock()
	defer p.mu.Unlock()
	op.at = len(p.ops)
	p.ops = append(p.ops, op)
}

// log returns every event so far, in order.
func (p *walProbe) log() []walOp {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.ops)
}

// ioFrom returns the writes and syncs of log whose call index is at least call, in log order.
func ioFrom(log []walOp, call int) []walOp {
	var out []walOp
	for _, op := range log {
		if (op.kind == "write" || op.kind == "sync") && op.call >= call {
			out = append(out, op)
		}
	}
	return out
}

// syncsOn returns the syncs of log on segment seg.
func syncsOn(log []walOp, seg string) []walOp {
	var out []walOp
	for _, op := range log {
		if op.kind == "sync" && op.seg == seg {
			out = append(out, op)
		}
	}
	return out
}

// writesOn returns the writes of log on segment seg.
func writesOn(log []walOp, seg string) []walOp {
	var out []walOp
	for _, op := range log {
		if op.kind == "write" && op.seg == seg {
			out = append(out, op)
		}
	}
	return out
}

// newWALIngest returns an unleased ingest on a fresh root, its WAL seams wired to a probe.
func newWALIngest(t *testing.T) (*ingest, *walProbe, string) {
	t.Helper()
	root := t.TempDir()
	ing := newIngest(root, config.Defaults(), logging.Nop(), nil, newFakeClock(epoch))
	t.Cleanup(func() { _ = ing.Close() })
	return ing, newWALProbe(ing), root
}

// walGate holds every Sync that calls hold until release; entered is closed when the first of them
// starts. newWALGate registers release as a cleanup, so a test that fails with a Sync still held
// lets its batch finish instead of leaving ingest.mu held for the cleanup's ing.Close to block on.
type walGate struct {
	entered, open       chan struct{}
	enterOnce, openOnce sync.Once
}

func newWALGate(t *testing.T) *walGate {
	g := &walGate{entered: make(chan struct{}), open: make(chan struct{})}
	t.Cleanup(g.release)
	return g
}

func (g *walGate) hold() {
	g.enterOnce.Do(func() { close(g.entered) })
	<-g.open
}

func (g *walGate) release() { g.openOnce.Do(func() { close(g.open) }) }

// awaitClosed waits for ch to be closed. The bound only turns a deadlock into a failure.
func awaitClosed(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-hangGuard(t):
		t.Fatalf("%s never happened", what)
	}
}

// walAccept is one Accept running on its own goroutine. err and recovered are final once done is
// closed.
type walAccept struct {
	id        int
	want      []byte
	done      chan struct{}
	err       error
	recovered any
}

// goAccept runs Accept for r on a new goroutine as request id. It logs the return, or the panic,
// on p the moment it happens, so the log orders every return against the WAL's writes and syncs.
func goAccept(ing *ingest, p *walProbe, id int, r walReq) *walAccept {
	a := &walAccept{id: id, want: r.want, done: make(chan struct{})}
	go func() {
		defer close(a.done)
		defer func() {
			if a.recovered = recover(); a.recovered != nil {
				p.add(walOp{kind: "panic", id: id})
			}
		}()
		a.err = ing.Accept(r.req, r.line)
		p.add(walOp{kind: "return", id: id, err: a.err})
	}()
	return a
}

func (a *walAccept) returned() bool {
	select {
	case <-a.done:
		return true
	default:
		return false
	}
}

// awaitAccept waits for a to return or panic. The bound only turns a deadlock into a failure.
func awaitAccept(t *testing.T, a *walAccept) {
	t.Helper()
	awaitClosed(t, a.done, "the return of Accept "+strconv.Itoa(a.id))
}

// awaitParked waits for goroutine id to be parked in reason, the wait reason runtime.Stack prints
// in a goroutine's header ("sync.Mutex.Lock", "sync.WaitGroup.Wait"), and reports true. It reports
// false instead as soon as done is closed first, which means the goroutine finished without ever
// waiting there. So a test can tell a call that is blocked from one that is merely slow without a
// timer deciding which; the bound only turns a goroutine that does neither into a failure.
func awaitParked(t *testing.T, id uint64, reason string, done <-chan struct{}) bool {
	t.Helper()
	buf := make([]byte, 64<<10)
	guard := hangGuard(t)
	for {
		select {
		case <-done:
			return false
		default:
		}
		n := runtime.Stack(buf, true)
		for n == len(buf) {
			buf = make([]byte, 2*len(buf))
			n = runtime.Stack(buf, true)
		}
		if parkedIn(buf[:n], id, reason) {
			return true
		}
		if hung(guard) {
			t.Fatalf("goroutine %d neither waited in %s nor finished", id, reason)
		}
		runtime.Gosched()
	}
}

// parkedIn reports whether the header line of goroutine id in dump, a runtime.Stack of every
// goroutine, names reason as its wait reason. The header is matched by its prefix and searched for
// the reason, because a higher GOTRACEBACK level adds fields between the two.
func parkedIn(dump []byte, id uint64, reason string) bool {
	prefix := []byte("goroutine " + strconv.FormatUint(id, 10) + " ")
	for line := range bytes.Lines(dump) {
		if bytes.HasPrefix(line, prefix) {
			return bytes.Contains(line, []byte("["+reason))
		}
	}
	return false
}

// queueBehind starts one Accept per request, in order and numbered from first, each only once the
// one before it has queued behind the batch in flight, so walQ's FIFO order is exactly reqs. A
// batch must be in flight. A correct Accept can only queue behind it. One that returns instead is
// a defect for the caller to assert on, so it is counted here rather than waited for; one that does
// neither, such as an Accept that blocks without going through walQ at all, fails the test once
// hangGuard has fired instead of hanging it.
func queueBehind(t *testing.T, ing *ingest, p *walProbe, first int, reqs []walReq) []*walAccept {
	t.Helper()
	out := make([]*walAccept, len(reqs))
	for k, r := range reqs {
		out[k] = goAccept(ing, p, first+k, r)
		guard := hangGuard(t)
		for {
			ing.walQ.mu.Lock()
			queued := len(ing.walQ.queue)
			ing.walQ.mu.Unlock()
			returned := 0
			for _, a := range out[:k+1] {
				if a.returned() {
					returned++
				}
			}
			if queued+returned >= k+1 {
				break
			}
			if hung(guard) {
				t.Fatalf("request %d neither queued behind the batch in flight nor returned", first+k)
			}
			runtime.Gosched()
		}
	}
	return out
}

func requireNoneReturned(t *testing.T, accepts []*walAccept, why string) {
	t.Helper()
	for _, a := range accepts {
		require.Falsef(t, a.returned(), "request %d returned %s", a.id, why)
	}
}

// requireDurableBeforeReturn checks invariant I1 on the probe's log: every Accept of accepts that
// returned nil did so only after a Write that carried its line, and then a Sync of that same
// handle, had both ended without error.
func requireDurableBeforeReturn(t *testing.T, p *walProbe, accepts []*walAccept) {
	t.Helper()
	log := p.log()
	for _, a := range accepts {
		if a.err != nil || a.recovered != nil {
			continue
		}
		ret := slices.IndexFunc(log, func(op walOp) bool { return op.kind == "return" && op.id == a.id })
		require.GreaterOrEqualf(t, ret, 0, "request %d's return is not in the log", a.id)
		w := slices.IndexFunc(log[:ret], func(op walOp) bool {
			return op.kind == "write" && op.err == nil && bytes.Contains(op.data, a.want)
		})
		require.GreaterOrEqualf(t, w, 0, "request %d returned nil before any Write carried its line", a.id)
		s := slices.IndexFunc(log[w:ret], func(op walOp) bool {
			return op.kind == "sync" && op.f == log[w].f && op.err == nil
		})
		require.GreaterOrEqualf(t, s, 0, "request %d returned nil before a Sync covering its line returned", a.id)
	}
}

// requireOutcomes checks each Accept against want: nil means it committed, and anything else is
// the error it must have failed with. None of them may have panicked.
func requireOutcomes(t *testing.T, got []*walAccept, want ...error) {
	t.Helper()
	require.Len(t, got, len(want))
	for k, a := range got {
		require.Nilf(t, a.recovered, "request %d panicked", a.id)
		if want[k] == nil {
			require.NoErrorf(t, a.err, "request %d", a.id)
			continue
		}
		require.ErrorIsf(t, a.err, want[k], "request %d", a.id)
	}
}

// T4 — design §6.2. A batch writes each WAL segment it touched once and syncs each of them once,
// with the Syncs of different segments in flight at the same time, and every line reaches its
// segment exactly as sent, with one terminator.
func TestIngest_WALGroupCommitOneSyncPerSegmentPerBatch(t *testing.T) {
	ing, p, root := newWALIngest(t)
	p.gate = newWALGate(t)
	const one, two = core.SessionID("gc-one"), core.SessionID("gc-two")

	// Batch 2's two Syncs wait for each other, so both get past this only if they are in flight
	// together. The bound turns a batch that syncs its segments one after the other into a
	// failure instead of a hang; it orders nothing.
	var arrived atomic.Int32
	together := make(chan struct{})
	var serial atomic.Bool
	p.onSync = func(call int, _ string, f *os.File) error {
		if call > 1 {
			if arrived.Add(1) == 2 {
				close(together)
			}
			select {
			case <-together:
			case <-hangGuard(t):
				serial.Store(true)
			}
		}
		return f.Sync()
	}

	first := newWALReq(t, one, 0)
	lead := goAccept(ing, p, 0, first)
	awaitClosed(t, p.gate.entered, "batch 1's Sync")
	order := make([]core.SessionID, 32)
	for k := range order {
		order[k] = one
		if k%2 == 1 {
			order[k] = two
		}
	}
	reqs := newWALReqs(t, 1, order...)
	accepts := queueBehind(t, ing, p, 1, reqs)
	p.gate.release()
	all := append([]*walAccept{lead}, accepts...)
	for _, a := range all {
		awaitAccept(t, a)
		require.Nilf(t, a.recovered, "request %d", a.id)
		require.NoErrorf(t, a.err, "request %d", a.id)
	}

	segOne, segTwo := segName(one, 0), segName(two, 0)
	require.Equal(t, int32(3), p.writes.Load(), "batch 1 writes one segment and batch 2 two: one Write per segment per batch")
	require.Equal(t, int32(3), p.syncs.Load(), "batch 1 syncs one segment and batch 2 two: one Sync per segment per batch")
	var writes, syncs []walOp
	for _, op := range ioFrom(p.log(), 2) {
		if op.kind == "write" {
			writes = append(writes, op)
		} else {
			syncs = append(syncs, op)
		}
	}
	require.Len(t, writes, 2)
	require.Len(t, syncs, 2)
	require.ElementsMatch(t, []string{segOne, segTwo}, []string{writes[0].seg, writes[1].seg})
	require.ElementsMatch(t, []string{segOne, segTwo}, []string{syncs[0].seg, syncs[1].seg})
	wantData := map[string]string{segOne: wantsOn(reqs, one), segTwo: wantsOn(reqs, two)}
	for _, w := range writes {
		require.NoError(t, w.err)
		require.Equalf(t, wantData[w.seg], string(w.data), "batch 2's one Write to %s carries every line queued for it, in order", w.seg)
	}
	for _, s := range syncs {
		require.NoError(t, s.err)
	}
	require.Less(t, max(writes[0].at, writes[1].at), min(syncs[0].at, syncs[1].at), "the batch writes every segment before it syncs any")
	require.False(t, serial.Load(), "the batch's two Syncs must be in flight together")

	require.Equal(t, string(first.want)+wantsOn(reqs, one), readWAL(t, root, one, 0), "exact bytes, one terminator per line")
	require.Equal(t, wantsOn(reqs, two), readWAL(t, root, two, 0), "exact bytes, one terminator per line")
	requireDurableBeforeReturn(t, p, all)
	requireIdle(t, &ing.walQ)
}

// T5 — design §6.2, invariant I1. While the Sync covering a line is held, the Accept that sent the
// line does not return and queues no job, and neither does any Accept queued behind it.
func TestIngest_AcceptNeverReturnsBeforeItsWALSync(t *testing.T) {
	ing, p, root := newWALIngest(t)
	const one, two = core.SessionID("i1-one"), core.SessionID("i1-two")
	batch1, batch2 := newWALGate(t), newWALGate(t)
	p.onSync = func(call int, _ string, f *os.File) error {
		if call == 1 {
			batch1.hold()
		} else {
			batch2.hold()
		}
		return f.Sync()
	}

	first := newWALReq(t, one, 0)
	lead := goAccept(ing, p, 0, first)
	awaitClosed(t, batch1.entered, "batch 1's Sync")
	require.False(t, lead.returned(), "Accept returned while the Sync covering its line was held")
	require.Empty(t, ing.ring, "a job was queued before the Sync covering its line returned")

	reqs := newWALReqs(t, 1, one, two, one, two, one, two, one, two)
	followers := queueBehind(t, ing, p, 1, reqs)
	requireNoneReturned(t, followers, "while the batch ahead of it held its Sync")
	require.Empty(t, ing.ring, "a job was queued before the Sync covering its line returned")

	batch1.release()
	awaitAccept(t, lead)
	require.NoError(t, lead.err)
	awaitClosed(t, batch2.entered, "batch 2's Sync")
	requireNoneReturned(t, followers, "while the Sync covering its line was held")
	require.Len(t, ing.ring, 1, "only the Accept whose Sync returned may have queued its job")

	batch2.release()
	for _, a := range followers {
		awaitAccept(t, a)
		require.Nilf(t, a.recovered, "request %d", a.id)
		require.NoErrorf(t, a.err, "request %d", a.id)
	}
	require.Len(t, ing.ring, 1+len(reqs), "every Accept queues its job once its line is durable")
	requireDurableBeforeReturn(t, p, append([]*walAccept{lead}, followers...))
	require.Equal(t, string(first.want)+wantsOn(reqs, one), readWAL(t, root, one, 0))
	require.Equal(t, wantsOn(reqs, two), readWAL(t, root, two, 0))
}

// The claim in ingest.mu's doc comment: a WAL batch holds the lock from its first line to its last
// Sync, so CloseSession and Close, which take it too, wait for the batch instead of closing a
// handle that it is writing or syncing.
func TestIngest_CloseWaitsForTheWALBatchInFlight(t *testing.T) {
	const sess = core.SessionID("close-one")
	for _, tc := range []struct {
		name string
		call func(*ingest) error
	}{
		{"CloseSession", func(ing *ingest) error { return ing.CloseSession(sess) }},
		{"Close", (*ingest).Close},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ing, p, root := newWALIngest(t)
			p.gate = newWALGate(t)
			r := newWALReq(t, sess, 0)
			lead := goAccept(ing, p, 0, r)
			awaitClosed(t, p.gate.entered, "the batch's Sync")
			if ing.mu.TryLock() {
				ing.mu.Unlock()
				t.Fatal("the batch let go of ingest.mu while its Sync was in flight")
			}

			ids, closed := make(chan uint64, 1), make(chan struct{})
			var closeErr error
			go func() {
				defer close(closed)
				ids <- goid()
				closeErr = tc.call(ing)
			}()
			require.Truef(t, awaitParked(t, <-ids, "sync.Mutex.Lock", closed),
				"%s returned while the batch was syncing the handle it closes", tc.name)

			p.gate.release()
			awaitAccept(t, lead)
			require.NoError(t, lead.err, "the batch's Sync ran on a handle that nothing had closed")
			awaitClosed(t, closed, "the return of "+tc.name)
			require.NoError(t, closeErr)
			ing.mu.Lock()
			_, cached := ing.wals[sess]
			ing.mu.Unlock()
			require.Falsef(t, cached, "%s drops the session's handle once the batch is done", tc.name)
			require.Equal(t, string(r.want), readWAL(t, root, sess, 0))
			requireDurableBeforeReturn(t, p, []*walAccept{lead})
		})
	}
}

// T6 — design §6.2, fix J-A7. A batch that crosses the rotation ceiling produces exactly the
// segments one append per line produces, and the lines it buffered for the outgoing segment are
// written to that segment's own handle and synced before the handle is closed.
func TestIngest_WALBatchPreservesRotationBoundaries(t *testing.T) {
	const rotA, rotB, gateSess = core.SessionID("rot-a"), core.SessionID("rot-b"), core.SessionID("rot-gate")
	const step = walTestLineSize + 1
	// Each session's current segment starts this many bytes in. rot-a's first line crosses the
	// ceiling with nothing of the batch buffered for it. rot-b has room for exactly two lines and
	// rotates at its third, with two lines of the batch buffered for the outgoing segment.
	start := map[core.SessionID]int64{rotA: walRotateBytes - 10, rotB: walRotateBytes - 2*step}
	nearCeiling := func(ing *ingest) {
		for sess, n := range start {
			wf := &walFile{}
			require.NoError(t, ing.openWALLocked(sess, wf))
			wf.bytes = n
			ing.wals[sess] = wf
		}
	}
	gate := newWALReq(t, gateSess, 0)
	reqs := newWALReqs(t, 1, rotA, rotB, rotA, rotB, rotA, rotB, rotA, rotB)

	// The reference: one Accept at a time, so that every append is a batch of one.
	refIng, _, refRoot := newWALIngest(t)
	nearCeiling(refIng)
	for _, r := range append([]walReq{gate}, reqs...) {
		require.NoError(t, refIng.Accept(r.req, r.line))
	}

	// The same Accepts, all eight in one batch behind a held batch 1.
	ing, p, root := newWALIngest(t)
	nearCeiling(ing)
	p.gate = newWALGate(t)
	lead := goAccept(ing, p, 0, gate)
	awaitClosed(t, p.gate.entered, "batch 1's Sync")
	accepts := queueBehind(t, ing, p, 1, reqs)
	p.gate.release()
	all := append([]*walAccept{lead}, accepts...)
	for _, a := range all {
		awaitAccept(t, a)
		require.Nilf(t, a.recovered, "request %d", a.id)
		require.NoErrorf(t, a.err, "request %d", a.id)
	}

	// Byte-identical segments, cut where a sequential append has always cut them.
	var bLines []walReq
	for _, r := range reqs {
		if r.req.Session == rotB {
			bLines = append(bLines, r)
		}
	}
	want := map[string]string{
		segName(gateSess, 0): string(gate.want),
		segName(rotA, 0):     "",
		segName(rotA, 1):     wantsOn(reqs, rotA),
		segName(rotB, 0):     wantsOn(bLines[:2], rotB),
		segName(rotB, 1):     wantsOn(bLines[2:], rotB),
	}
	for _, sess := range []core.SessionID{gateSess, rotA, rotB} {
		for seq := 0; seq <= 2; seq++ {
			name := segName(sess, seq)
			got, gotErr := os.ReadFile(paths.Long(walPath(paths.Of(root).Spool, sess, seq)))
			ref, refErr := os.ReadFile(paths.Long(walPath(paths.Of(refRoot).Spool, sess, seq)))
			require.Equalf(t, os.IsNotExist(refErr), os.IsNotExist(gotErr), "segment %s exists in one run only", name)
			require.Equalf(t, string(ref), string(got), "segment %s differs from one append per line", name)
			w, ok := want[name]
			if !ok {
				require.Truef(t, os.IsNotExist(gotErr), "segment %s must not exist", name)
				continue
			}
			require.NoError(t, gotErr)
			require.Equalf(t, w, string(got), "segment %s", name)
		}
	}
	for _, sess := range []core.SessionID{rotA, rotB} {
		refIng.mu.Lock()
		ref := *refIng.wals[sess]
		refIng.mu.Unlock()
		ing.mu.Lock()
		got := *ing.wals[sess]
		ing.mu.Unlock()
		require.Equalf(t, 1, got.seq, "session %s rotated once", sess)
		require.Equalf(t, ref.seq, got.seq, "session %s", sess)
		require.Equalf(t, ref.bytes, got.bytes, "session %s", sess)
	}

	// Batch 2's I/O: the outgoing segment is written and then synced on its own handle before
	// anything else, and only then are the two open segments written, then synced.
	oldB, newA, newB := segName(rotB, 0), segName(rotA, 1), segName(rotB, 1)
	io2 := ioFrom(p.log(), 2)
	events := make([]string, len(io2))
	for k, op := range io2 {
		events[k] = op.kind + " " + op.seg
		require.NoErrorf(t, op.err, "%s", events[k])
	}
	require.Lenf(t, io2, 6, "batch 2's I/O: %v", events)
	require.Equal(t, []string{"write " + oldB, "sync " + oldB}, events[:2], "the outgoing segment is written and synced first")
	require.Equal(t, wantsOn(bLines[:2], rotB), string(io2[0].data), "the lines counted against the outgoing segment are written to it")
	require.Same(t, io2[0].f, io2[1].f, "the outgoing segment is synced on the handle its lines were written to")
	require.ElementsMatch(t, []string{"write " + newA, "write " + newB}, events[2:4])
	require.ElementsMatch(t, []string{"sync " + newA, "sync " + newB}, events[4:])
	// That Sync returned nil, and a Sync on a closed *os.File fails, so it ran before the rotation
	// closed the handle. The handle is closed now.
	require.ErrorIs(t, io2[1].f.Sync(), os.ErrClosed, "the rotation must close the outgoing segment's handle")
	requireDurableBeforeReturn(t, p, all)
}

// walTestPanic is what a Sync panics with in T7, naming its segment.
type walTestPanic struct{ seg string }

// T7 — design §6.2. A write error, a short write or a sync error fails exactly the lines buffered
// for its one segment, every other segment in the batch commits, and a failed line stays failed
// when a later Sync of the same file succeeds. A Sync that panics, on the leader or on a helper
// goroutine, leaves no Accept of its batch returning nil: T3's J-A2 regression, through Accept.
func TestIngest_WALFailureFailsExactlyItsSegment(t *testing.T) {
	const bad, good, gateSess = core.SessionID("f-bad"), core.SessionID("f-good"), core.SessionID("f-gate")
	badSeg := segName(bad, 0)
	errFault := errors.New("private WAL fault fixture")

	// batch holds batch 1 on gateSess, queues one request per session of order behind it, lets
	// batch 1 finish, and returns the queued requests and their Accepts once all have returned.
	batch := func(t *testing.T, ing *ingest, p *walProbe, order ...core.SessionID) ([]walReq, []*walAccept) {
		t.Helper()
		p.gate = newWALGate(t)
		lead := goAccept(ing, p, 0, newWALReq(t, gateSess, 0))
		awaitClosed(t, p.gate.entered, "batch 1's Sync")
		reqs := newWALReqs(t, 1, order...)
		got := queueBehind(t, ing, p, 1, reqs)
		p.gate.release()
		awaitAccept(t, lead)
		require.NoError(t, lead.err)
		for _, a := range got {
			awaitAccept(t, a)
		}
		return reqs, got
	}
	// later is one more Accept on sess once the fault is spent; it must commit.
	later := func(t *testing.T, ing *ingest, sess core.SessionID) walReq {
		t.Helper()
		r := newWALReq(t, sess, 99)
		require.NoError(t, ing.Accept(r.req, r.line), "an Accept after the failed batch must commit")
		return r
	}
	// oneLineShort puts sess's current segment exactly one test line short of the rotation ceiling,
	// so the second line a batch takes for sess rotates with the first still buffered.
	oneLineShort := func(t *testing.T, ing *ingest, sess core.SessionID) {
		t.Helper()
		wf := &walFile{}
		require.NoError(t, ing.openWALLocked(sess, wf))
		wf.bytes = walRotateBytes - (walTestLineSize + 1)
		ing.wals[sess] = wf
	}

	t.Run("a write error", func(t *testing.T) {
		ing, p, root := newWALIngest(t)
		var armed atomic.Bool
		armed.Store(true)
		p.onWrite = func(_ int, seg string, f *os.File, b []byte) (int, error) {
			if seg == badSeg && armed.CompareAndSwap(true, false) {
				return 0, errFault
			}
			return f.Write(b)
		}
		_, got := batch(t, ing, p, bad, good, bad, good)
		requireOutcomes(t, got, errFault, nil, errFault, nil)
		require.Empty(t, syncsOn(p.log(), badSeg), "a segment whose Write failed is not synced")
		require.Zero(t, walBytes(ing, bad), "a failed Write counts no bytes into its segment")
		r := later(t, ing, bad)
		require.Equal(t, string(r.want), readWAL(t, root, bad, 0))
	})

	t.Run("a short write", func(t *testing.T) {
		ing, p, root := newWALIngest(t)
		var armed atomic.Bool
		armed.Store(true)
		p.onWrite = func(_ int, seg string, f *os.File, b []byte) (int, error) {
			if seg == badSeg && armed.CompareAndSwap(true, false) {
				return f.Write(b[:len(b)/2]) // part of the buffer, reported without an error
			}
			return f.Write(b)
		}
		reqs, got := batch(t, ing, p, bad, good, bad, good)
		requireOutcomes(t, got, io.ErrShortWrite, nil, io.ErrShortWrite, nil)
		require.Empty(t, syncsOn(p.log(), badSeg), "a segment whose Write fell short is not synced")
		buffered := wantsOn(reqs, bad)
		written := buffered[:len(buffered)/2]
		require.Equal(t, int64(len(written)), walBytes(ing, bad),
			"a short Write counts the bytes it did write, as a sequential append always has")
		r := later(t, ing, bad)
		require.Equal(t, written+string(r.want), readWAL(t, root, bad, 0))
	})

	t.Run("a sync error", func(t *testing.T) {
		ing, p, root := newWALIngest(t)
		var armed atomic.Bool
		armed.Store(true)
		p.onSync = func(_ int, seg string, f *os.File) error {
			if seg == badSeg && armed.CompareAndSwap(true, false) {
				return errFault
			}
			return f.Sync()
		}
		reqs, got := batch(t, ing, p, bad, good, bad, good)
		requireOutcomes(t, got, errFault, nil, errFault, nil)
		// The failed lines were written, so the next Sync of the same file covers them too. Their
		// Accepts failed all the same: a result is final once its batch returns.
		r := later(t, ing, bad)
		require.Equal(t, wantsOn(reqs, bad)+string(r.want), readWAL(t, root, bad, 0))
	})

	t.Run("a sync error on a batch's only segment", func(t *testing.T) {
		// A batch whose lines all belong to one segment syncs it on the leader, with no helper
		// goroutine. Every isolated Accept is such a batch, so this is the path most lines take.
		ing, p, root := newWALIngest(t)
		var failing atomic.Bool
		failing.Store(true)
		p.onSync = func(_ int, seg string, f *os.File) error {
			if seg == badSeg && failing.Load() {
				return errFault
			}
			return f.Sync()
		}
		reqs, got := batch(t, ing, p, bad, bad)
		requireOutcomes(t, got, errFault, errFault)
		lone := newWALReq(t, bad, len(reqs)+1)
		require.ErrorIs(t, ing.Accept(lone.req, lone.line), errFault, "an isolated Accept whose Sync failed must fail")
		require.Len(t, syncsOn(p.log(), badSeg), 2, "each of the two batches syncs the segment once")
		failing.Store(false)
		r := later(t, ing, bad)
		require.Equal(t, wantsOn(reqs, bad)+string(lone.want)+string(r.want), readWAL(t, root, bad, 0))
	})

	t.Run("a rotation's sync error fails only the outgoing segment", func(t *testing.T) {
		ing, p, root := newWALIngest(t)
		oneLineShort(t, ing, bad)
		var armed atomic.Bool
		armed.Store(true)
		p.onSync = func(_ int, seg string, f *os.File) error {
			if seg == badSeg && armed.CompareAndSwap(true, false) {
				return errFault
			}
			return f.Sync()
		}
		reqs, got := batch(t, ing, p, bad, good, bad, good)
		requireOutcomes(t, got, errFault, nil, nil, nil)
		require.Equal(t, string(reqs[0].want), readWAL(t, root, bad, 0), "the outgoing segment holds the line its Sync failed")
		require.Equal(t, string(reqs[2].want), readWAL(t, root, bad, 1), "the next segment holds the line past the ceiling")
	})

	for _, fault := range []struct {
		name  string
		write func(f *os.File, b []byte) (int, error)
		want  error
		kept  func(buffered string) string // what the outgoing segment holds of the buffer it was sent
	}{
		{
			name:  "write error",
			write: func(*os.File, []byte) (int, error) { return 0, errFault },
			want:  errFault,
			kept:  func(string) string { return "" },
		},
		{
			name:  "short write",
			write: func(f *os.File, b []byte) (int, error) { return f.Write(b[:len(b)/2]) },
			want:  io.ErrShortWrite,
			kept:  func(buffered string) string { return buffered[:len(buffered)/2] },
		},
	} {
		t.Run("a rotation flush's "+fault.name+" fails only the outgoing segment", func(t *testing.T) {
			ing, p, root := newWALIngest(t)
			oneLineShort(t, ing, bad)
			var armed atomic.Bool
			armed.Store(true)
			p.onWrite = func(_ int, seg string, f *os.File, b []byte) (int, error) {
				if seg == badSeg && armed.CompareAndSwap(true, false) {
					return fault.write(f, b)
				}
				return f.Write(b)
			}
			reqs, got := batch(t, ing, p, bad, good, bad, good)
			requireOutcomes(t, got, fault.want, nil, nil, nil)
			require.Empty(t, syncsOn(p.log(), badSeg), "a segment whose Write failed is not synced, not even ahead of its Close")
			require.Equal(t, fault.kept(string(reqs[0].want)), readWAL(t, root, bad, 0))
			require.Equal(t, string(reqs[2].want), readWAL(t, root, bad, 1), "the next segment holds the line past the ceiling")
			requireDurableBeforeReturn(t, p, got)
		})
	}

	t.Run("a line after a rotation whose open failed goes to the segment it reopens", func(t *testing.T) {
		// A directory at bad's next segment path makes bad's rotation fail to open it, and good's
		// rotation flush, which the batch runs between bad's rotation and bad's last line, removes
		// the directory. bad's last line then reopens the segment and must be written there: joined
		// to the buffer that bad's rotation already flushed, it would be acknowledged in no file.
		ing, p, root := newWALIngest(t)
		oneLineShort(t, ing, bad)
		oneLineShort(t, ing, good)
		blocker := paths.Long(walPath(paths.Of(root).Spool, bad, 1))
		require.NoError(t, os.Mkdir(blocker, 0o700))
		goodSeg := segName(good, 0)
		removed := make(chan error, 1)
		p.onWrite = func(_ int, seg string, f *os.File, b []byte) (int, error) {
			if seg == goodSeg {
				select {
				case removed <- os.Remove(blocker):
				default:
				}
			}
			return f.Write(b)
		}
		// bad fits; good fits; bad rotates and its open fails; good rotates, and its flush removes
		// the directory; bad reopens its segment 1.
		reqs, got := batch(t, ing, p, bad, good, bad, good, bad)
		select {
		case err := <-removed:
			require.NoError(t, err, "good's rotation flush clears bad's next segment path")
		default:
			t.Fatal("good's rotation flush never ran")
		}
		requireOutcomes(t, got[:2], nil, nil)
		require.Nil(t, got[2].recovered)
		require.ErrorContains(t, got[2].err, "open wal", "bad's rotation cannot open its next segment")
		requireOutcomes(t, got[3:], nil, nil)
		require.Equal(t, string(reqs[0].want), readWAL(t, root, bad, 0))
		require.Equal(t, string(reqs[4].want), readWAL(t, root, bad, 1),
			"the line acknowledged after the reopen is in the segment it reopened")
		require.Equal(t, string(reqs[1].want), readWAL(t, root, good, 0))
		require.Equal(t, string(reqs[3].want), readWAL(t, root, good, 1))
		requireDurableBeforeReturn(t, p, got)
	})

	t.Run("a rotation whose Close fails fails its session's lines", func(t *testing.T) {
		// A sequential append leaves the session on the handle its rotation's Close failed on, so
		// each later line of the session fails the same way and none is written; a batch does the
		// same. The handle is closed behind the ingest's back, which makes that Close fail.
		ing, p, root := newWALIngest(t)
		wf := &walFile{}
		require.NoError(t, ing.openWALLocked(bad, wf))
		wf.bytes = walRotateBytes - 10 // bad's next line crosses the ceiling with nothing buffered
		ing.wals[bad] = wf
		require.NoError(t, wf.w.Close())
		_, got := batch(t, ing, p, bad, good, bad, good)
		requireOutcomes(t, got, os.ErrClosed, nil, os.ErrClosed, nil)
		require.Empty(t, writesOn(p.log(), badSeg), "no line of a session whose rotation failed is written")
		require.Empty(t, syncsOn(p.log(), badSeg))
		_, err := os.Stat(paths.Long(walPath(paths.Of(root).Spool, bad, 1)))
		require.Truef(t, os.IsNotExist(err), "a rotation whose Close failed opens no next segment: %v", err)
		later(t, ing, good)
		requireIdle(t, &ing.walQ)
	})

	t.Run("a Sync that panics on the leader", func(t *testing.T) {
		ing, p, _ := newWALIngest(t)
		var armed atomic.Bool
		armed.Store(true)
		p.onSync = func(_ int, seg string, f *os.File) error {
			if seg == badSeg && armed.CompareAndSwap(true, false) {
				panic(walTestPanic{seg: seg})
			}
			return f.Sync()
		}
		_, got := batch(t, ing, p, bad, bad)
		require.Equal(t, walTestPanic{seg: badSeg}, got[0].recovered, "the panic continues on the leader's goroutine")
		require.Nil(t, got[1].recovered)
		require.ErrorIs(t, got[1].err, errNotCommitted, "a follower of a batch that panicked must not report its line durable")
		later(t, ing, bad)
		requireIdle(t, &ing.walQ)
	})

	t.Run("a Sync that panics on a helper goroutine", func(t *testing.T) {
		ing, p, _ := newWALIngest(t)
		var armed atomic.Bool
		armed.Store(true)
		p.onSync = func(_ int, seg string, f *os.File) error {
			if seg == badSeg && armed.CompareAndSwap(true, false) {
				panic(walTestPanic{seg: seg})
			}
			return f.Sync()
		}
		// The leader's own line opens the first segment, so it syncs that one itself and the
		// failing segment's Sync runs on a helper.
		_, got := batch(t, ing, p, good, bad, good, bad)
		require.Equal(t, walTestPanic{seg: badSeg}, got[0].recovered, "a helper's panic is re-raised on the leader, after the join")
		for _, a := range got[1:] {
			require.Nilf(t, a.recovered, "request %d", a.id)
			require.ErrorIsf(t, a.err, errNotCommitted,
				"request %d: no Accept of a batch that panicked may report its line durable, even where its own Sync returned", a.id)
		}
		later(t, ing, bad)
		later(t, ing, good)
		requireIdle(t, &ing.walQ)
	})

	// runtime.Goexit stands in, in the next two subtests, for a Sync that leaves its goroutine
	// without returning. A production (*os.File).Sync cannot, but a seam can, and so can any
	// future path: no Sync covering a line returned, so the line must not count as durable.
	t.Run("a Sync that never returns on a helper goroutine", func(t *testing.T) {
		ing, p, _ := newWALIngest(t)
		var armed atomic.Bool
		armed.Store(true)
		p.onSync = func(_ int, seg string, f *os.File) error {
			if seg == badSeg && armed.CompareAndSwap(true, false) {
				runtime.Goexit()
			}
			return f.Sync()
		}
		// The leader's own line opens the first segment, so the failing segment's Sync runs on a
		// helper.
		_, got := batch(t, ing, p, good, bad, good, bad)
		requireOutcomes(t, got, nil, errNotCommitted, nil, errNotCommitted)
		require.Empty(t, syncsOn(p.log(), badSeg), "no Sync of the failing segment returned")
		requireDurableBeforeReturn(t, p, got)
		later(t, ing, bad)
		requireIdle(t, &ing.walQ)
	})

	t.Run("a Sync that never returns on the leader", func(t *testing.T) {
		// The leader's own Sync leaves its goroutine while a helper's Sync is still in flight. The
		// leader must still join the helper before it lets go of ingest.mu, or the next batch could
		// rotate or close the handle the helper is syncing.
		ing, p, _ := newWALIngest(t)
		p.gate = newWALGate(t)
		helper := newWALGate(t)
		goodSeg := segName(good, 0)
		leader := make(chan uint64, 1)
		var armed atomic.Bool
		armed.Store(true)
		p.onSync = func(_ int, seg string, f *os.File) error {
			switch {
			case seg == badSeg:
				helper.hold()
			case seg == goodSeg && armed.CompareAndSwap(true, false):
				leader <- goid()
				runtime.Goexit()
			}
			return f.Sync()
		}
		lead := goAccept(ing, p, 0, newWALReq(t, gateSess, 0))
		awaitClosed(t, p.gate.entered, "batch 1's Sync")
		got := queueBehind(t, ing, p, 1, newWALReqs(t, 1, good, bad))
		p.gate.release()
		awaitAccept(t, lead)
		require.NoError(t, lead.err)
		awaitClosed(t, helper.entered, "the helper's Sync")
		var id uint64
		select {
		case id = <-leader:
		case <-hangGuard(t):
			t.Fatal("the leader's own Sync never started")
		}
		require.True(t, awaitParked(t, id, "sync.WaitGroup.Wait", got[0].done),
			"the leader left its batch while a helper's Sync was still in flight")
		if ing.mu.TryLock() {
			ing.mu.Unlock()
			t.Fatal("ingest.mu was released while a helper's Sync was still in flight")
		}
		helper.release()
		for _, a := range got {
			awaitAccept(t, a)
			require.Nilf(t, a.recovered, "request %d", a.id)
		}
		require.False(t, slices.ContainsFunc(p.log(), func(op walOp) bool { return op.kind == "return" && op.id == got[0].id }),
			"the leader's Accept never returned")
		require.ErrorIs(t, got[1].err, errNotCommitted, "a batch whose leader never finished resolves none of its lines")
		later(t, ing, bad)
		later(t, ing, good)
		requireIdle(t, &ing.walQ)
	})
}

// T8 — design §6.2, rejected option X1. A delivery's lease line is written to the journal only
// after the WAL Sync covering that delivery's line has returned, while the WAL group-commits under
// concurrent Accepts, so Accept never makes a lease durable before the delivery's bytes. This pins
// the Accept path; the drain's half is TestDrainNeverLeasesAWALLineBeforeItsSyncReturns and
// TestDrainTakesNothingOfAFreshSegmentBeforeItsFirstSyncReturns.
func TestIngest_LeaseLineNeverPrecedesItsWALSync(t *testing.T) {
	root, lock, journal := newTestDeliveryJournal(t)
	ing := newIngest(root, config.Defaults(), logging.Nop(), nil, newFakeClock(epoch))
	t.Cleanup(func() { _ = ing.Close() })
	ing.journal = lock.openDeliveryJournal
	p := newWALProbe(ing)
	p.gate = newWALGate(t)

	sessions := []core.SessionID{"x1-one", "x1-two"}
	reqs := make([]walReq, 17)
	nonces := make([]string, len(reqs))
	for k := range reqs {
		nonce, err := ipc.NewDeliveryNonce()
		require.NoError(t, err)
		req := ipc.Request{
			Op: ipc.OpObserveTool, Session: sessions[k%len(sessions)], TS: benchLeasedBaseTS + core.UnixMilli(k), Nonce: nonce,
		}
		line, err := ipc.EncodeRequest(req)
		require.NoError(t, err)
		reqs[k] = walReq{req: req, line: line, want: line} // the wire line already ends in the one terminator
		nonces[k] = nonce
	}
	carried := func(b []byte) []string {
		var out []string
		for _, nonce := range nonces {
			if bytes.Contains(b, []byte(nonce)) {
				out = append(out, nonce)
			}
		}
		return out
	}

	// pending holds, per WAL segment, the deliveries whose lines have been written to it and not yet
	// covered by a Sync that returned. A Sync that returns logs each of them as durable.
	var mu sync.Mutex
	pending := map[string][]string{}
	p.onWrite = func(_ int, seg string, f *os.File, b []byte) (int, error) {
		n, err := f.Write(b)
		mu.Lock()
		pending[seg] = append(pending[seg], carried(b[:max(n, 0)])...)
		mu.Unlock()
		return n, err
	}
	p.onSync = func(_ int, seg string, f *os.File) error {
		if err := f.Sync(); err != nil {
			return err
		}
		mu.Lock()
		covered := pending[seg]
		delete(pending, seg)
		mu.Unlock()
		for _, nonce := range covered {
			p.add(walOp{kind: "wal-durable", what: nonce})
		}
		return nil
	}
	journal.writer = leaseFaultWriter{file: journal.file, write: func(b []byte) (int, error) {
		for _, nonce := range carried(b) {
			p.add(walOp{kind: "journal-write", what: nonce})
		}
		return journal.file.Write(b)
	}}

	lead := goAccept(ing, p, 0, reqs[0])
	awaitClosed(t, p.gate.entered, "batch 1's Sync")
	followers := queueBehind(t, ing, p, 1, reqs[1:])
	p.gate.release()
	all := append([]*walAccept{lead}, followers...)
	for _, a := range all {
		awaitAccept(t, a)
		require.Nilf(t, a.recovered, "request %d", a.id)
		require.NoErrorf(t, a.err, "request %d", a.id)
	}
	require.Equal(t, int32(3), p.syncs.Load(), "the queued deliveries share batch 2's Syncs, one per segment")

	lock.mu.Lock()
	leased := len(journal.leases)
	lock.mu.Unlock()
	require.Equal(t, len(reqs), leased, "every delivery takes a lease; an unleased Accept would leave nothing to order")

	durableAt, writtenAt := map[string]int{}, map[string]int{}
	for _, op := range p.log() {
		switch op.kind {
		case "wal-durable":
			if _, seen := durableAt[op.what]; !seen {
				durableAt[op.what] = op.at
			}
		case "journal-write":
			_, dup := writtenAt[op.what]
			require.Falsef(t, dup, "delivery %s wrote a second lease line", op.what)
			writtenAt[op.what] = op.at
		}
	}
	for k, nonce := range nonces {
		w, ok := writtenAt[nonce]
		require.Truef(t, ok, "delivery %d never reached the journal", k)
		d, ok := durableAt[nonce]
		require.Truef(t, ok, "delivery %d's WAL line was never covered by a Sync that returned", k)
		require.Lessf(t, d, w, "delivery %d's lease line was written before the WAL Sync covering its line returned", k)
	}
	requireDurableBeforeReturn(t, p, all)
}

// newWALReqOfSize builds request k on sess in newWALReq's shape, as a line of exactly size bytes
// before its terminator, and hands Accept the wire form.
func newWALReqOfSize(t *testing.T, sess core.SessionID, k, size int) walReq {
	t.Helper()
	head := `{"s":"` + string(sess) + `","k":` + strconv.Itoa(k) + `,"p":"`
	const tail = `"}`
	pad := size - len(head) - len(tail)
	require.GreaterOrEqual(t, pad, 0, "the session name leaves no room in the line")
	wire := []byte(head + strings.Repeat("x", pad) + tail + "\n")
	return walReq{req: ipc.Request{Op: ipc.OpObserveTool, Session: sess}, line: wire, want: wire}
}

// walCapLines holds batch 1 on a session of its own, queues reqs behind it in order, releases it,
// and requires every Accept to commit. reqs must all be on one other session and stay short of its
// rotation ceiling, so each later batch writes that session's one segment in one Write and syncs it
// once. It returns how many lines each of those Writes carried, batch 2's first, once it has checked
// that together they carried every line of reqs exactly once, in order.
func walCapLines(t *testing.T, reqs []walReq) []int {
	t.Helper()
	sess := reqs[0].req.Session
	ing, p, root := newWALIngest(t)
	p.gate = newWALGate(t)
	lead := goAccept(ing, p, 0, newWALReq(t, "cap-gate", 0))
	awaitClosed(t, p.gate.entered, "batch 1's Sync")
	accepts := queueBehind(t, ing, p, 1, reqs)
	requireNoneReturned(t, accepts, "while the batch ahead of it held its Sync")
	p.gate.release()
	all := append([]*walAccept{lead}, accepts...)
	for _, a := range all {
		awaitAccept(t, a)
		require.Nilf(t, a.recovered, "request %d", a.id)
		require.NoErrorf(t, a.err, "request %d", a.id)
	}
	requireDurableBeforeReturn(t, p, all)
	requireIdle(t, &ing.walQ)

	seg := segName(sess, 0)
	var lines []int
	var carried []byte
	syncs := 0
	for _, op := range ioFrom(p.log(), 2) {
		require.NoErrorf(t, op.err, "%s %s", op.kind, op.seg)
		require.Equalf(t, seg, op.seg, "every batch after the first writes and syncs %s only", seg)
		if op.kind == "sync" {
			syncs++
			continue
		}
		lines = append(lines, bytes.Count(op.data, []byte{'\n'}))
		carried = append(carried, op.data...)
	}
	require.Equal(t, len(lines), syncs, "each batch after the first writes its one segment once and syncs it once")
	// Compared as booleans: a diff of two 4 MiB strings would bury the failure.
	want := wantsOn(reqs, sess)
	require.True(t, want == string(carried), "the Writes after batch 1 carry every queued line once, in order")
	require.True(t, want == readWAL(t, root, sess, 0), "the segment holds every queued line once, in order")
	return lines
}

// Design §2.3's request cap, through Accept: a WAL batch holds at most groupCommitMaxRequests
// requests. One more than that, all short and on one session, queued behind a held batch commit as
// a full batch and then a batch of one: at today's cap, 513 lines give 512, then 1.
func TestIngest_WALBatchStopsAtTheRequestCap(t *testing.T) {
	const sess = core.SessionID("cap-count")
	order := make([]core.SessionID, groupCommitMaxRequests+1)
	for k := range order {
		order[k] = sess
	}
	require.Equal(t, []int{groupCommitMaxRequests, 1}, walCapLines(t, newWALReqs(t, 1, order...)),
		"lines per Write after batch 1: the request cap ends batch 2, and the request past it commits in batch 3")
}

// Design §2.3's WAL byte cap, through Accept: a WAL batch's lines, each counted with its one
// terminator as walItemSize counts it, sum to at most walGroupCommitMaxBytes. The lines below fill
// the cap exactly without their terminators (256 × 16 384 = 4 194 304 at today's cap), so a size
// that left the terminator out would cut all of them into one batch. Counting it, 255 fit
// (255 × 16 385 = 4 178 175) and a 256th would make 4 194 560 > 4 194 304, so batch 2 carries all
// but the last line and batch 3 that one.
func TestIngest_WALBatchByteCapCountsEveryTerminator(t *testing.T) {
	const sess = core.SessionID("cap-bytes")
	const lines = groupCommitMaxRequests / 2 // under the request cap, so only the byte cap can end a batch
	const lineSize = walGroupCommitMaxBytes / lines
	require.Equal(t, walGroupCommitMaxBytes, lines*lineSize, "the lines fill the byte cap exactly without their terminators")
	require.LessOrEqual(t, (lines-1)*(lineSize+1), walGroupCommitMaxBytes, "all but one of the lines fit with their terminators")
	reqs := make([]walReq, lines)
	for k := range reqs {
		reqs[k] = newWALReqOfSize(t, sess, 1+k, lineSize)
	}
	require.Equal(t, []int{lines - 1, 1}, walCapLines(t, reqs),
		"lines per Write after batch 1: the byte cap, counting each terminator, ends batch 2 one line short")
}

// The same cap at its other edge: lines that fill it EXACTLY, each terminator counted. The case above
// fills the cap without the terminators, so it catches an under-count and no over-count: every
// per-line over-count from 1 to 64 bytes still cuts [255 1], and so does a cap one byte low. Here
// walGroupCommitMaxBytes/fillLines lines of one byte less than that sum, terminators included, to the
// cap itself, so all of them commit together and the one past them starts the next batch. An
// over-count of a single byte gives [255 2] instead, and so do a cap one byte low and a comparison
// written "<" where walItemSize's total must be admitted at "<=".
func TestIngest_WALBatchByteCapAdmitsLinesThatFillItExactly(t *testing.T) {
	const sess = core.SessionID("cap-exact")
	const fillLines = groupCommitMaxRequests / 2          // under the request cap, so only the byte cap can end a batch
	const lineSize = walGroupCommitMaxBytes/fillLines - 1 // with its one terminator: walGroupCommitMaxBytes/fillLines
	require.Equal(t, walGroupCommitMaxBytes, fillLines*(lineSize+1),
		"the lines fill the byte cap exactly, each terminator counted")
	require.Less(t, fillLines+1, groupCommitMaxRequests, "one more line than that is still under the request cap")
	reqs := make([]walReq, fillLines+1)
	for k := range reqs {
		reqs[k] = newWALReqOfSize(t, sess, 1+k, lineSize)
	}
	require.Equal(t, []int{fillLines, 1}, walCapLines(t, reqs),
		"lines per Write after batch 1: the cap admits the lines that fill it exactly, and the next one commits in batch 3")
}

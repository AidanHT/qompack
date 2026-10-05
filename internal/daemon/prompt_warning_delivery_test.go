package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/grammar"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/observer"
	"github.com/qompack/qompack/internal/paths"
)

// A thrash warning is drained from the observer's queue by the prompt reply that carries it, and its
// rule is marked warned when it is queued. These rows pin what happens when that reply does NOT reach
// the host: the daemon's own wait ran out (l0_prompt_reply_late), or the client's ran out first while
// the daemon was still making the prompt durable. Either way the warning must not count as
// delivered. It is not replayed into a later turn either — it described the loop as it stood at that
// prompt — but the rule warns afresh once the loop continues.
//
// The daemon here is the real one, wired to the real observer and a real Sequitur grammar
// (WireObserver), and the tool uses are folded in process, so the queue state is exact. No ingest
// worker runs, so the only ObservePrompt calls are the reply path's own, except where a row runs the
// queued worker job itself (drainRing).

// warnDeliverySess is the session every row here drives.
const warnDeliverySess = core.SessionID("sess-warn-delivery")

// warnLoopCycles is how many Read→Edit→Bash cycles queue the warning: the rule is queued the first
// time its reference count exceeds the observer's thrashMinUses (3).
const warnLoopCycles = 4

// warnDeliveryAcRepeated is the multiplicity a warning renders: the rule's reference count when it
// was queued.
func warnDeliveryAcRepeated(n int) string { return fmt.Sprintf("repeated %d×", n) }

// replyGate holds one reply-path ObservePrompt call: it signals entry, waits for release, and
// signals when the observer has answered.
type replyGate struct {
	entered chan struct{}
	release chan struct{}
	done    chan struct{}
}

func newReplyGate() *replyGate {
	return &replyGate{entered: make(chan struct{}, 1), release: make(chan struct{}), done: make(chan struct{}, 1)}
}

// warnDeliveryRig is a wired daemon with a hold seam in front of the observer's reply path (gate)
// and one behind it (held): held lets the observer answer, and so claim the reply, and then holds
// the reply before the daemon renders it.
type warnDeliveryRig struct {
	t      *testing.T
	root   string
	obsv   observer.Observer
	dd     *daemon
	gate   atomic.Pointer[replyGate]
	held   atomic.Pointer[replyGate]
	nextID int
	spacer int
}

func newWarnDeliveryRig(t *testing.T) *warnDeliveryRig {
	t.Helper()
	r := &warnDeliveryRig{t: t, root: t.TempDir()}
	o := NewOptions(r.root, testConfig())
	o.Grammar = grammar.New()
	obsv, err := WireObserver(&o)
	require.NoError(t, err)
	r.obsv = obsv
	t.Cleanup(func() { _ = o.Store.Close() })
	t.Cleanup(func() {
		if o.Ledger != nil {
			_ = o.Ledger.Close()
		}
	})
	// Bound after WireObserver's own bind, so it wraps the observer's seam.
	o.Bind(func(s *Services) {
		inner := s.ObservePrompt
		s.ObservePrompt = func(ctx context.Context, e hookio.Event) (hookio.Output, error) {
			if !observer.PromptReplyOnly(ctx) {
				return inner(ctx, e)
			}
			if h := r.held.Load(); h != nil {
				out, err := inner(ctx, e)
				h.entered <- struct{}{}
				<-h.release
				h.done <- struct{}{}
				return out, err
			}
			g := r.gate.Load()
			if g == nil {
				return inner(ctx, e)
			}
			g.entered <- struct{}{}
			<-g.release
			out, err := inner(ctx, e)
			g.done <- struct{}{}
			return out, err
		}
	})
	d, err := New(o)
	require.NoError(t, err)
	dd, ok := d.(*daemon)
	require.True(t, ok)
	t.Cleanup(func() { _ = dd.ing.Close() })
	r.dd = dd
	return r
}

// tool folds one PostToolUse into the observer, in process and in order.
func (r *warnDeliveryRig) tool(name string, input, response map[string]any) {
	r.t.Helper()
	r.nextID++
	in, err := json.Marshal(input)
	require.NoError(r.t, err)
	resp, err := json.Marshal(response)
	require.NoError(r.t, err)
	_, err = r.obsv.OnToolUse(context.Background(), hookio.Event{
		HookEventName: "PostToolUse", SessionID: warnDeliverySess, CWD: r.root, ToolName: name,
		ToolUseID: core.ToolUseID(fmt.Sprintf("toolu_warn_%03d", r.nextID)), ToolInput: in, ToolResponse: resp,
	})
	require.NoError(r.t, err)
}

// spacerTool folds one tool whose display symbol occurs nowhere else in the stream, so no digram
// involving it ever repeats and the loop rule stays referenced once per cycle.
func (r *warnDeliveryRig) spacerTool() {
	r.t.Helper()
	r.spacer++
	r.tool(fmt.Sprintf("WarnSpacer%02d", r.spacer), map[string]any{"query": "errExpired"}, map[string]any{"content": "no result"})
}

// cycle folds one Read→Edit→Bash cycle against the same file with the same failing build.
func (r *warnDeliveryRig) cycle() {
	r.t.Helper()
	r.tool("Read", map[string]any{"file_path": "src/auth.go"},
		map[string]any{"content": "package auth\n\nfunc refresh() error { return errExpired }\n"})
	r.tool("Edit", map[string]any{"file_path": "src/auth.go", "old_string": "errExpired", "new_string": "errExpired"},
		map[string]any{"content": "The file src/auth.go has been updated."})
	r.tool("Bash", map[string]any{"command": "go build ./..."},
		map[string]any{"exit_code": 1, "stdout": "", "stderr": "./src/auth.go:3: undefined: errExpired"})
}

// loop folds n cycles, each after the first separated from the one before by a spacer.
func (r *warnDeliveryRig) loop(n int) {
	r.t.Helper()
	for i := range n {
		if i > 0 {
			r.spacerTool()
		}
		r.cycle()
	}
}

// promptAt sends one observe.prompt through the daemon's own route, stamped ts by the "hook".
func (r *warnDeliveryRig) promptAt(text string, ts core.UnixMilli) hookio.Output {
	r.t.Helper()
	ev := &hookio.Event{HookEventName: "UserPromptSubmit", SessionID: warnDeliverySess, CWD: r.root, Prompt: text}
	resp := r.dd.dispatchOp(context.Background(), ipc.Request{
		Op: ipc.OpObservePrompt, Session: warnDeliverySess, Reply: true, Event: ev, TS: ts,
	})
	require.True(r.t, resp.OK)
	require.NotNil(r.t, resp.Output)
	return *resp.Output
}

// prompt sends one observe.prompt with no hook stamp, so its reply gets the whole deadline from the
// route (promptReplyBudget): the rows that are not about lateness do not depend on how long this
// machine's WAL fsync takes. The stamped path is promptAt's, and TestPromptReplyBudget_* pins it.
func (r *warnDeliveryRig) prompt(text string) hookio.Output {
	r.t.Helper()
	return r.promptAt(text, 0)
}

func (r *warnDeliveryRig) late() int64 { return r.dd.m.Counter(counterPromptReplyLate).Value() }

// requireLoopWarning asserts out is exactly one thrash warning for the loop at multiplicity n.
func requireLoopWarning(t *testing.T, out hookio.Output, n int, msg string) {
	t.Helper()
	require.NotNil(t, out.HookSpecificOutput, msg)
	ac := out.HookSpecificOutput.AdditionalContext
	require.True(t, strings.HasPrefix(ac, "[qompack] possible loop:"), "%s: %q", msg, ac)
	require.NotContains(t, ac, "\n", "%s: one rule renders as one line: %q", msg, ac)
	require.Contains(t, ac, "FileRead→FileEdit→Bash", "%s: %q", msg, ac)
	require.Contains(t, ac, warnDeliveryAcRepeated(n), "%s: %q", msg, ac)
}

// TestPromptWarning_DeliveredReplyCountsOnce is the control: a reply inside its deadline carries the
// warning once, and the rule is never reported again, even as the loop continues.
func TestPromptWarning_DeliveredReplyCountsOnce(t *testing.T) {
	r := newWarnDeliveryRig(t)
	r.loop(warnLoopCycles)
	requireLoopWarning(t, r.prompt("still red"), warnLoopCycles, "the first prompt after the loop carries the warning")
	require.Zero(t, r.late())

	r.spacerTool()
	r.cycle()
	out := r.prompt("and again")
	require.Nil(t, out.HookSpecificOutput, "a delivered warning is once per rule, however long the loop runs")
}

// TestPromptWarning_LateReplyDoesNotConsumeTheRule: the reply path is held past the daemon's reply
// deadline, so the reply goes out empty and is counted late. The observer answers afterwards; the
// warning it drained then must not count as delivered.
func TestPromptWarning_LateReplyDoesNotConsumeTheRule(t *testing.T) {
	r := newWarnDeliveryRig(t)
	r.loop(warnLoopCycles)

	g := newReplyGate()
	r.gate.Store(g)
	out := r.prompt("still red")
	require.Nil(t, out.HookSpecificOutput, "a reply past its deadline goes out empty")
	require.Equal(t, int64(1), r.late(), "and is counted late")
	awaitSignal(t, g.entered, "the reply path never called the observer")
	close(g.release)
	awaitSignal(t, g.done, "the held reply call never finished")
	r.gate.Store(nil)

	// The design keeps: a stale warning is not replayed into a later turn.
	require.Nil(t, r.prompt("what now?").HookSpecificOutput,
		"a warning whose reply went out empty must not reappear on a later prompt by itself")
	r.spacerTool()
	require.Nil(t, r.prompt("moved on").HookSpecificOutput,
		"a session that left the loop is not warned about it")

	// The fix: the loop continues, so the rule warns afresh, at its current multiplicity.
	r.spacerTool()
	r.cycle()
	requireLoopWarning(t, r.prompt("still red after another try"), warnLoopCycles+1,
		"a warning the host never received must not count as delivered once the loop continues")
	require.Equal(t, int64(1), r.late())

	r.spacerTool()
	r.cycle()
	require.Nil(t, r.prompt("and again").HookSpecificOutput, "once delivered, the rule is reported once")
}

// TestPromptWarning_SlowDurableAcceptIsLateForTheClient: the prompt's WAL fsync is held until the
// client's whole reply budget, measured from the instant the hook stamped the request, has passed.
// The client has stopped waiting by then, so the daemon must not send — and must not consume — the
// warning; it counts the reply late, and the rule warns afresh once the loop continues.
func TestPromptWarning_SlowDurableAcceptIsLateForTheClient(t *testing.T) {
	r := newWarnDeliveryRig(t)
	r.loop(warnLoopCycles)

	ts := core.NowMilli(r.dd.clk)
	spent := ts + core.UnixMilli(promptReplyDeadline.Milliseconds())
	sync := r.dd.ing.syncWAL
	r.dd.ing.syncWAL = func(f *os.File) error {
		// A slowed fsync: held until the hook's reply budget is spent on the daemon's own clock.
		for core.NowMilli(r.dd.clk) < spent {
			<-time.After(time.Until(time.UnixMilli(int64(spent))) + time.Millisecond)
		}
		return sync(f)
	}
	out := r.promptAt("still red", ts)
	r.dd.ing.syncWAL = sync
	require.Nil(t, out.HookSpecificOutput,
		"a reply produced after the hook's budget ran out reaches nobody; it must go out empty")
	require.Equal(t, int64(1), r.late(), "and be counted late")

	require.Nil(t, r.prompt("what now?").HookSpecificOutput, "the stale warning is not replayed")
	r.spacerTool()
	r.cycle()
	requireLoopWarning(t, r.prompt("still red after another try"), warnLoopCycles+1,
		"the rule warns afresh once the loop continues")
}

// TestPromptWarning_ReplyThatMayNotActRefusesTheClaim: a reply the daemon will not let act (the
// mode it read at the route's start forbids it) delivers nothing, so a warning the observer would
// drain for it is re-armed rather than consumed — even when the observer itself reads ModeFull.
func TestPromptWarning_ReplyThatMayNotActRefusesTheClaim(t *testing.T) {
	r := newWarnDeliveryRig(t)
	r.loop(warnLoopCycles)
	ev := &hookio.Event{HookEventName: "UserPromptSubmit", SessionID: warnDeliverySess, CWD: r.root, Prompt: "still red"}
	out := r.dd.callObservePromptWithDeadline(context.Background(), ev, core.NowMilli(r.dd.clk), "", false)
	require.Nil(t, out.HookSpecificOutput)
	r.dd.promptWG.Wait() // the reply call has answered, whichever side of the deadline it landed on
	require.Equal(t, int64(1), r.dd.m.Counter(observerThrashUndelivered).Value(),
		"the warning the observer had queued is re-armed, not consumed")

	r.spacerTool()
	r.cycle()
	requireLoopWarning(t, r.prompt("still red after another try"), warnLoopCycles+1,
		"the rule warns afresh once the loop continues")
}

// warnSpooledFile is the client spool the "hook" of TestPromptWarning_ClaimedReplyTheHookSpooledIsReArmed
// falls back to, named the way ipc names a hook process's own spool.
const warnSpooledFile = "client-04242.ndjson"

// TestPromptWarning_ClaimedReplyTheHookSpooledIsReArmed is the window w16d-warnlate left open (its
// mode (b) residual). The observer claims the reply inside the deadline, so the daemon's reply
// carries the warning and nothing is counted late; but the reply is held, after the claim and before
// the daemon renders it, while the hook gives up and spools the prompt. The host never saw the
// warning. The exact signal is the drain settling that client-spooled copy, whose nonce the daemon
// already answered live: it must re-arm the rule, so the loop warns afresh if it continues.
//
// The request carries no hook stamp, as the rig's prompt does, so the claim gets the whole deadline
// from the route and does not depend on how long this machine's WAL fsync takes. The hook's give-up
// is the test's own step (it writes the spool while the reply is held), not a clock.
func TestPromptWarning_ClaimedReplyTheHookSpooledIsReArmed(t *testing.T) {
	r := newWarnDeliveryRig(t)
	lock := lockFor(t, r.dd, r.root)
	t.Cleanup(func() { _ = lock.Release() })
	r.dd.drain.Store(newDrainer(contentDrainConfig(r.dd)))
	r.loop(warnLoopCycles)
	undelivered := func() int64 { return r.dd.m.Counter(observerThrashUndelivered).Value() }

	req := ipc.Request{
		Op: ipc.OpObservePrompt, Session: warnDeliverySess, Reply: true, Nonce: testDeliveryToken('a'),
		Event: &hookio.Event{HookEventName: "UserPromptSubmit", SessionID: warnDeliverySess, CWD: r.root, Prompt: "still red"},
	}
	h := newReplyGate()
	r.held.Store(h)
	replied := make(chan ipc.Response, 1)
	go func() { replied <- r.dd.dispatchOp(context.Background(), req) }()
	awaitSignal(t, h.entered, "the observer never answered the reply path")

	// The claim has been made and the reply is held: the hook's own wait runs out meanwhile, and it
	// gives up and spools the prompt.
	writeSpoolLine(t, r.root, warnSpooledFile, req)
	close(h.release)
	awaitSignal(t, h.done, "the held reply call never finished")
	var resp ipc.Response
	select {
	case resp = <-replied:
	case <-hangGuard(t):
		require.FailNow(t, "the claimed reply never went out")
	}
	r.held.Store(nil)
	require.True(t, resp.OK)
	require.NotNil(t, resp.Output)
	requireLoopWarning(t, *resp.Output, warnLoopCycles, "the claim won, so the daemon's reply carries the warning")
	require.Zero(t, r.late(), "a claimed reply is not counted late: the daemon cannot see that the hook gave up")
	require.Zero(t, undelivered())

	// The worker captures the live copy and acknowledges its nonce; the drain then absorbs the hook's
	// spooled copy without dispatching it — and that is the moment the loss becomes known.
	drainRing(t, r.dd)
	n, err := r.dd.Drain(context.Background())
	require.NoError(t, err)
	require.Zero(t, n, "the spooled copy of a prompt the daemon answered live is absorbed, not dispatched")
	_, statErr := os.Stat(paths.Long(filepath.Join(paths.Of(r.root).Spool, warnSpooledFile)))
	require.ErrorIs(t, statErr, fs.ErrNotExist, "the absorbed client spool is released")
	require.Equal(t, int64(1), undelivered(),
		"a warning whose reply the hook spooled was never seen: it is re-armed, not counted as delivered")

	// As for a refused claim: the stale warning is not replayed, and the rule warns afresh only once
	// the loop continues.
	require.Nil(t, r.prompt("what now?").HookSpecificOutput,
		"a warning whose reply reached no hook must not reappear on a later prompt by itself")
	r.spacerTool()
	r.cycle()
	requireLoopWarning(t, r.prompt("still red after another try"), warnLoopCycles+1,
		"a warning the host never received must not count as delivered once the loop continues")
	r.spacerTool()
	r.cycle()
	require.Nil(t, r.prompt("and again").HookSpecificOutput, "once delivered, the rule is reported once")
	require.Equal(t, int64(1), undelivered())
}

// observerThrashUndelivered is internal/observer's counterThrashUndelivered, respelled because it is
// unexported there.
const observerThrashUndelivered = "observer.thrash_undelivered"

func TestPromptReplyHandoff_FirstSideWins(t *testing.T) {
	claimedFirst := new(promptReplyHandoff)
	require.True(t, claimedFirst.claim())
	require.False(t, claimedFirst.abandon(), "a claimed reply carries the warning whatever the clock says after")

	abandonedFirst := new(promptReplyHandoff)
	require.True(t, abandonedFirst.abandon())
	require.True(t, abandonedFirst.abandon(), "abandoning twice is still abandoned")
	require.False(t, abandonedFirst.claim(), "nothing may be claimed for a reply that went out empty")
}

func TestPromptReplyBudget_IsMeasuredFromTheHooksStamp(t *testing.T) {
	r := newWarnDeliveryRig(t)
	now := core.NowMilli(r.dd.clk)
	require.Equal(t, promptReplyDeadline, r.dd.promptReplyBudget(0), "no stamp: the whole deadline")
	require.Equal(t, promptReplyDeadline,
		r.dd.promptReplyBudget(now+core.UnixMilli(hotPathSampleFutureSlop.Milliseconds())+core.UnixMilli(time.Minute.Milliseconds())),
		"a stamp from the future cannot be trusted: the whole deadline")
	require.Equal(t, promptReplyDeadline,
		r.dd.promptReplyBudget(now-core.UnixMilli(hotPathSampleMaxAge.Milliseconds())-core.UnixMilli(time.Minute.Milliseconds())),
		"an implausibly old stamp cannot be trusted: the whole deadline")

	spent := 100 * time.Millisecond
	got := r.dd.promptReplyBudget(now - core.UnixMilli(spent.Milliseconds()))
	require.LessOrEqual(t, got, promptReplyDeadline-spent, "the hook's time before the daemon is spent")
	require.Positive(t, got)
	require.LessOrEqual(t, r.dd.promptReplyBudget(now-core.UnixMilli(promptReplyDeadline.Milliseconds())), time.Duration(0),
		"a stamp a whole deadline old leaves nothing to wait for")
}

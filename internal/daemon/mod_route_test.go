package daemon

// The mod route: OpMod requests reach serveBrief with the values the CLI sent. These rows drive
// dispatchOp, as the server does, with a fake snapshot seam; serveBrief's own sequence is pinned in
// brief_route_test.go.

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/modwire"
)

const modRouteSession = "sess-mod-route"

// modRouteItems are what the fake snapshot returns: two items, the second far longer than the first.
var modRouteItems = []checkpoint.BriefItem{
	{Kind: checkpoint.BriefInvariant, ID: "inv_1", Text: "Never log access tokens."},
	{Kind: checkpoint.BriefInvariant, ID: "inv_2", Text: strings.Repeat("y", 200)},
}

// snapshotProbe is a fake BriefSnapshot seam that records what it was called with.
type snapshotProbe struct {
	mu       sync.Mutex
	calls    int
	sessions []core.SessionID
	deadline time.Time
	hasDL    bool
	// block makes the seam wait for its context instead of answering.
	block bool
}

func (p *snapshotProbe) seam(ctx context.Context, sess core.SessionID) (checkpoint.Brief, error) {
	p.mu.Lock()
	p.calls++
	p.sessions = append(p.sessions, sess)
	p.deadline, p.hasDL = ctx.Deadline()
	block := p.block
	p.mu.Unlock()
	if block {
		<-ctx.Done()
		return checkpoint.Brief{}, ctx.Err()
	}
	return checkpoint.Brief{Items: append([]checkpoint.BriefItem(nil), modRouteItems...)}, nil
}

func (p *snapshotProbe) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

// newModRouteDaemon is a bare daemon with the probe bound as its snapshot seam. mutate, when set,
// changes the configuration the daemon starts with.
func newModRouteDaemon(t *testing.T, mutate func(*config.Config)) (*daemon, *snapshotProbe) {
	t.Helper()
	cfg := testConfig()
	if mutate != nil {
		mutate(&cfg)
	}
	d, err := New(Options{ProjectRoot: t.TempDir(), Cfg: cfg, Log: logging.Nop()})
	require.NoError(t, err)
	dd, ok := d.(*daemon)
	require.True(t, ok)
	t.Cleanup(func() { _ = dd.Stop(context.Background()) })
	p := &snapshotProbe{}
	dd.svc.BriefSnapshot = p.seam
	return dd, p
}

// modBriefRequest is a brief request for the route's session.
func modBriefRequest(trigger string) modwire.BriefRequest {
	return modwire.BriefRequest{
		V: modwire.Version, SessionID: modRouteSession, CWD: "/p", HostVersion: modwire.MinHostVersion,
		Trigger: trigger,
	}
}

// modCallRequest builds the OpMod request the CLI sends for req, with the daemon's budget.
func modCallRequest(t *testing.T, verb string, budgetMs int64, req any) ipc.Request {
	t.Helper()
	b, err := json.Marshal(req)
	require.NoError(t, err)
	r, err := ipc.ModRequest(modRouteSession, core.NowMilli(core.SystemClock()), ipc.ModCall{
		Verb: verb, BudgetMs: budgetMs, Req: b,
	})
	require.NoError(t, err)
	return r
}

// modAnswer reads a served exchange's answer: an OK response whose Data is one brief response.
func modAnswer(t *testing.T, resp ipc.Response) modwire.BriefResponse {
	t.Helper()
	require.True(t, resp.OK, "the route answered: %+v", resp)
	var b modwire.BriefResponse
	require.NoError(t, json.Unmarshal(resp.Data, &b), "data: %s", resp.Data)
	require.NoError(t, b.Validate(modwire.BriefMaxChars), "the answer obeys the wire contract")
	return b
}

// modGenerousBudgetMs is a budget no row's fake snapshot comes near.
const modGenerousBudgetMs = modwire.TimeoutMs

func TestHandleMod_ServesABriefThroughServeBrief(t *testing.T) {
	dd, p := newModRouteDaemon(t, nil)

	resp := dd.dispatchOp(context.Background(),
		modCallRequest(t, "brief", modGenerousBudgetMs, modBriefRequest(modwire.TriggerPrecompute)))

	b := modAnswer(t, resp)
	require.True(t, b.OK)
	require.True(t, strings.HasPrefix(b.Instructions, modwire.BriefMarker+"\n"))
	require.Equal(t, []string{"inv_1", "inv_2"}, b.FactIDs)
	require.Equal(t, []core.SessionID{modRouteSession}, p.sessions, "the request's session is the one briefed")
	require.Equal(t, int64(1), dd.m.Counter(counterBriefServed).Value())
}

// TestHandleMod_TheDeadlineIsReceiptPlusTheBudget: the snapshot runs under a deadline the CLI's
// budget sets, counted from when the daemon received the request.
func TestHandleMod_TheDeadlineIsReceiptPlusTheBudget(t *testing.T) {
	dd, p := newModRouteDaemon(t, nil)
	const budget = 1500 * time.Millisecond

	before := time.Now()
	modAnswer(t, dd.dispatchOp(context.Background(),
		modCallRequest(t, "brief", budget.Milliseconds(), modBriefRequest(modwire.TriggerPrecompute))))
	after := time.Now()

	require.True(t, p.hasDL, "the snapshot has a deadline")
	require.False(t, p.deadline.Before(before.Add(budget)), "deadline %v is receipt plus the budget", p.deadline)
	require.False(t, p.deadline.After(after.Add(budget)), "deadline %v is receipt plus the budget", p.deadline)
}

// TestHandleMod_ABudgetIsCappedAtTheModsTimeout: no budget gives the route longer than the mod waits.
func TestHandleMod_ABudgetIsCappedAtTheModsTimeout(t *testing.T) {
	dd, p := newModRouteDaemon(t, nil)

	modAnswer(t, dd.dispatchOp(context.Background(),
		modCallRequest(t, "brief", int64(time.Hour/time.Millisecond), modBriefRequest(modwire.TriggerPrecompute))))
	after := time.Now()

	require.True(t, p.hasDL)
	require.False(t, p.deadline.After(after.Add(modwire.TimeoutMs*time.Millisecond)), "deadline %v", p.deadline)
}

// TestHandleMod_ABudgetThatIsOverAnswersTimeout: a budget that is not positive is already over; the
// route answers timeout and starts nothing.
func TestHandleMod_ABudgetThatIsOverAnswersTimeout(t *testing.T) {
	for _, budget := range []int64{0, -5} {
		dd, p := newModRouteDaemon(t, nil)

		b := modAnswer(t, dd.dispatchOp(context.Background(),
			modCallRequest(t, "brief", budget, modBriefRequest(modwire.TriggerManual))))

		require.False(t, b.OK, "budget %d", budget)
		require.Equal(t, modwire.ReasonTimeout, b.Reason)
		require.Zero(t, p.count(), "no snapshot for a budget that is over")
	}
}

// TestHandleMod_ASnapshotThatOutlivesTheBudgetIsATimeout: the route's own timeout answer is
// forwarded unchanged, within the budget.
func TestHandleMod_ASnapshotThatOutlivesTheBudgetIsATimeout(t *testing.T) {
	dd, p := newModRouteDaemon(t, nil)
	p.block = true

	b := modAnswer(t, dd.dispatchOp(context.Background(),
		modCallRequest(t, "brief", 100, modBriefRequest(modwire.TriggerPrecompute))))

	require.False(t, b.OK)
	require.Equal(t, modwire.ReasonTimeout, b.Reason)
	require.Equal(t, int64(1), dd.m.Counter(counterBriefTimeout).Value())
}

// TestHandleMod_TheCapIsTheLiveConfiguration: runtime.mod.briefMaxChars caps the instructions.
func TestHandleMod_TheCapIsTheLiveConfiguration(t *testing.T) {
	const cap80 = 80
	dd, _ := newModRouteDaemon(t, func(c *config.Config) { c.Runtime.Mod.BriefMaxChars = cap80 })

	b := modAnswer(t, dd.dispatchOp(context.Background(),
		modCallRequest(t, "brief", modGenerousBudgetMs, modBriefRequest(modwire.TriggerPrecompute))))

	require.True(t, b.OK)
	require.Equal(t, []string{"inv_1"}, b.FactIDs, "only the item that fits the configured cap")
	require.LessOrEqual(t, modwire.CharCount(b.Instructions), cap80)
}

// TestHandleMod_RefusesWhatIsNotABrief: a call the route cannot read is refused with an error, and
// nothing is snapshotted.
func TestHandleMod_RefusesWhatIsNotABrief(t *testing.T) {
	malformedBrief := modBriefRequest(modwire.TriggerManual)
	malformedBrief.Trigger = "not a trigger"
	for name, req := range map[string]ipc.Request{
		"no call": {Op: ipc.OpMod, Session: modRouteSession, Reply: true},
		"hello is not routed": modCallRequest(t, "hello", modGenerousBudgetMs, modwire.HelloRequest{
			V: modwire.Version, SessionID: modRouteSession, CWD: "/p", HostVersion: modwire.MinHostVersion,
		}),
		"unknown verb":    modCallRequest(t, "augment", modGenerousBudgetMs, modBriefRequest(modwire.TriggerManual)),
		"malformed brief": modCallRequest(t, "brief", modGenerousBudgetMs, malformedBrief),
		"other version": modCallRequest(t, "brief", modGenerousBudgetMs, map[string]any{
			"v": 2, "session_id": modRouteSession, "cwd": "/p", "host_version": "2.1.300", "trigger": "auto",
		}),
	} {
		t.Run(name, func(t *testing.T) {
			dd, p := newModRouteDaemon(t, nil)
			resp := dd.dispatchOp(context.Background(), req)
			require.False(t, resp.OK)
			require.NotEmpty(t, resp.Err)
			require.NotContains(t, resp.Err, "unknown op", "the mod route itself refuses it")
			require.Empty(t, resp.Data)
			require.Zero(t, p.count())
		})
	}
}

// TestHandleMod_AReplayedBriefIsRefused: a mod request is never spooled, and a brief answered late
// has no caller, so one that reaches the route from a spool replay is refused untouched.
func TestHandleMod_AReplayedBriefIsRefused(t *testing.T) {
	dd, p := newModRouteDaemon(t, nil)

	resp := dd.drainDispatch(context.Background(),
		modCallRequest(t, "brief", modGenerousBudgetMs, modBriefRequest(modwire.TriggerManual)))

	require.False(t, resp.OK)
	require.Contains(t, resp.Err, "replayed")
	require.Zero(t, p.count())
}

// TestHandleMod_TheConfiguredDefaultIsTheWireCeiling holds config's restated ceiling to modwire's.
func TestHandleMod_TheConfiguredDefaultIsTheWireCeiling(t *testing.T) {
	require.Equal(t, modwire.BriefMaxChars, config.ModBriefMaxCharsCeiling)
	require.Equal(t, modwire.BriefMaxChars, config.Defaults().Runtime.Mod.BriefMaxChars)
}

// TestHandleMod_TheDeadlineNeverOutlivesTheCLIsWait: the request's TS is the CLI process's start,
// so the CLI stops waiting at TS + TimeoutMs - RecordMarginMs; the route's deadline is never later
// than that less DaemonReplyMarginMs, however long the dial and the transit took to deliver the
// budget. Here TS is 1,000 ms old and the budget 1,500 ms, so the deadline is TS + 1,700 ms, about
// 700 ms after receipt rather than 1,500.
func TestHandleMod_TheDeadlineNeverOutlivesTheCLIsWait(t *testing.T) {
	dd, p := newModRouteDaemon(t, nil)
	req := modCallRequest(t, "brief", 1500, modBriefRequest(modwire.TriggerPrecompute))
	ts := time.Now().Add(-time.Second)
	req.TS = core.UnixMilli(ts.UnixMilli())

	modAnswer(t, dd.dispatchOp(context.Background(), req))

	layer1 := time.Duration(modwire.TimeoutMs-modwire.RecordMarginMs-modwire.DaemonReplyMarginMs) * time.Millisecond
	require.True(t, p.hasDL)
	require.Equal(t, time.UnixMilli(int64(req.TS)).Add(layer1), p.deadline,
		"the deadline is the CLI's start plus the daemon's layer, not receipt plus the budget")
}

// TestHandleMod_TheCLIsCapLowersTheLiveOne: the envelope carries the cap the CLI's own configuration
// set; the route renders to the smaller of it and its live value, whichever side holds the lower one.
func TestHandleMod_TheCLIsCapLowersTheLiveOne(t *testing.T) {
	const cap80 = 80
	for name, tc := range map[string]struct{ live, cli int }{
		"the CLI's is lower": {live: modwire.BriefMaxChars, cli: cap80},
		"the live is lower":  {live: cap80, cli: modwire.BriefMaxChars},
		"the CLI sent none":  {live: cap80, cli: 0},
	} {
		t.Run(name, func(t *testing.T) {
			dd, _ := newModRouteDaemon(t, func(c *config.Config) { c.Runtime.Mod.BriefMaxChars = tc.live })
			b, err := json.Marshal(modBriefRequest(modwire.TriggerPrecompute))
			require.NoError(t, err)
			req, err := ipc.ModRequest(modRouteSession, core.NowMilli(core.SystemClock()), ipc.ModCall{
				Verb: "brief", BudgetMs: modGenerousBudgetMs, MaxChars: tc.cli, Req: b,
			})
			require.NoError(t, err)

			got := modAnswer(t, dd.dispatchOp(context.Background(), req))

			require.Equal(t, []string{"inv_1"}, got.FactIDs, "only the item that fits the smaller cap")
			require.LessOrEqual(t, modwire.CharCount(got.Instructions), cap80)
		})
	}
}

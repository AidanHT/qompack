// V5 §4.1 — the L0 observer → daemon metrics/registry → `qompack status --json` round trip
// (SP-08 × SP-05 × SP-14).
//
// Current criterion (plans/V5-VERIFY §4, row 4.1): "SP-14 status agrees with authoritative
// qualified observations and missing telemetry." The authoritative observation is the daemon's own
// ipc.OpStatus payload, read directly over the transport with the same helper the daemon e2e rows
// use (e2eStatus), and — once the daemon is gone — the metrics/latency.json snapshot that daemon
// persisted on its way out. Status must render exactly those numbers, qualify every one with where
// it came from, and say "unavailable, because ..." for every row nothing measured, without ever
// inventing a value for it.
//
// Every component is the production one: the real binary's hook subcommands against a daemon the
// first hook lazily spawned (the path a user walks — not the in-process v4 rig, because status's
// whole seam is a second process asking the resident one over the wire), the real metrics
// registry, the real persisted snapshot, and `qompack status --json` as its own third process.
//
// Retired clauses this row must NOT assert (see plans/sdd/V5-VERIFY/x01-disposition.md): the
// historical `data.store.*`, `data.sketches.*`, `data.frontier.*`, `data.mode.*` and
// `data.unavailable` members do not exist on this tree; the `p99 < 15ms` latency bound is a
// co-load-sensitive performance figure §5 forbids as an acceptance fact; and the historical
// per-hook `observe_tool` latency row is exactly the six-identical-figures claim SP-14 refused to
// make — status now reports those hooks as unavailable with the reason.
package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/commands"
	"github.com/qompack/qompack/internal/contract"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/daemon"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/obs"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/pluginmanifest"
	"github.com/qompack/qompack/internal/testutil"
)

// x1v5Session is this row's session identity, distinct from every other file's.
const x1v5Session = core.SessionID("sess-e2e-v5-x01")

// The historical §4.1 event mix: 60 tool uses, 4 prompts, one subagent stop. All three are hot-path
// ops (ipc.Op.HotPath), so each delivery that reaches the daemon live is one sample in the B-A
// hook_controlled histogram and one ingest.Accept sample in B-B. session-start is not a hot-path op
// and contributes to neither, which is what makes the expected count exactly the sum below.
const (
	x1v5ToolEvents    = 60
	x1v5PromptEvents  = 4
	x1v5StopEvents    = 1
	x1v5HotPathEvents = x1v5ToolEvents + x1v5PromptEvents + x1v5StopEvents

	// x1v5DeliveryEvents is the part of the mix whose index record production guarantees: a tool
	// or stop delivery is NAKed when the daemon cannot process it in time, spools, and is replayed
	// by Drain until its record lands. A prompt is not — see x1v5PromptPutErrCounter.
	x1v5DeliveryEvents = x1v5ToolEvents + x1v5StopEvents
)

// x1v5PromptPutErrCounter is the daemon-served counter that reconciles the index against the
// prompt half of the mix. Since the V6 SP08-D3 fix the observe.prompt reply path records nothing:
// it returns only the thrash warning, under internal/daemon/handlers.go promptReplyDeadline. The
// verbatim capture runs in the ingest worker off the WAL line the route appended
// (internal/daemon/daemon.go runIngested), and a WAL or client-spool replay of an observe.prompt
// runs that same capture, so a later Drain does recover a prompt the worker did not capture. A
// capture whose verbatim store.PutBytes fails bumps this counter (internal/observer/prompt.go,
// stagePromptPut): a leased delivery is then left unacknowledged for a later replay, and an
// unleased one is soft-dropped for good, never with a LOUD line and never a spool. So production
// promises 61 index records, not 65, and the wait below is on records plus this counter. The name
// is internal/observer/observer.go counterErrPrefix ("observer.err.") + prompt.go stagePromptPut
// ("prompt.put"), spelled here because both are unexported.
const x1v5PromptPutErrCounter = "observer.err.prompt.put"

// x1v5ObserverErrPrefix selects the observer's soft-failure counters for the wait diagnostic
// (internal/observer/observer.go counterErrPrefix).
const x1v5ObserverErrPrefix = "observer.err."

// x1v5L0AcceptErrCounter is the daemon's own count of hot-path WAL appends that failed
// (internal/daemon/handlers.go counterL0AcceptError), rendered by the wait diagnostic so a
// delivery the daemon could not even append is named as such.
const x1v5L0AcceptErrCounter = "l0_accept_error"

// x1v5HookControlledObserved is the daemon-owned name of the measured lower bound on B-A. It is
// served in the status snapshot beside the budgets' own histograms but is deliberately not a §2.4
// budget row (internal/daemon/handlers.go histHookControlledObserved). Spelled here because the
// daemon keeps it unexported; the test below asserts it is present and NOT a budget row, so a
// rename would fail loudly rather than silently pass.
const x1v5HookControlledObserved = "hook_controlled_observed"

// x1v5LatencyFile is the persisted metrics snapshot obs.Registry.Persist writes and the status
// command's fallback when no daemon answers (internal/cli/qompack_commands.go latencyFile).
const x1v5LatencyFile = "latency.json"

// x1v5HistOf returns the histogram name obs.Budgets() associates with id, looked up rather than
// spelled, so the budget table stays the single source of truth (mirrors e2eIngestHistName).
func x1v5HistOf(t *testing.T, id obs.BudgetID) string {
	t.Helper()
	for _, b := range obs.Budgets() {
		if b.ID == id {
			return b.Hist
		}
	}
	require.FailNowf(t, "budget table changed", "obs.Budgets() no longer declares %s", id)
	return ""
}

// x1v5RunStatus runs `qompack status --json` through the real binary and returns the decoded
// status.full report. The command always exits 0 and always answers ok:true — status exists to
// say what could and could not be observed, and "nothing could be reached" is one of its answers
// (internal/commands/cmd_status.go) — so both are asserted here for every arm alike.
func x1v5RunStatus(t *testing.T, bin string, p *testutil.Project) commands.StatusReport {
	t.Helper()
	stdout, stderr, code := Run(t, bin, []string{"status", "--json"}, nil, e2eEnv(p))
	require.Equal(t, 0, code, "qompack status --json must exit 0\nstdout:\n%s\nstderr:\n%s", stdout, stderr)

	env, err := commands.DecodeEnvelope(stdout)
	require.NoError(t, err, "stdout must be one command envelope:\n%s", stdout)
	require.Equal(t, commands.EnvelopeSchema, env.Schema)
	require.Equal(t, "status", env.Command)
	require.True(t, env.OK, "status never fails for want of an observation; error=%+v", env.Error)
	require.Nil(t, env.Error)

	var rep commands.StatusReport
	require.NoError(t, json.Unmarshal(env.Data, &rep), "envelope data must be a StatusReport:\n%s", env.Data)
	require.Equal(t, commands.StatusSchema, rep.Schema)
	require.Equal(t, len(pluginmanifest.HookEntryPoints()), len(rep.Hooks),
		"one hook row per installed entry point, in hooks.json order")
	require.Equal(t, len(obs.Budgets()), len(rep.Budgets), "one budget row per §2.4 budget, in B-A..B-G order")
	return rep
}

// x1v5RequireQualified asserts the one rule every row obeys in every arm: a value is displayed if
// and only if it was observed, and a row with no value says why. It fails on a value without
// availability, availability without a value, a missing reason, or a provenance that disagrees
// with the report's primary source.
func x1v5RequireQualified(t *testing.T, rep commands.StatusReport) {
	t.Helper()
	check := func(what string, lat *commands.Latency, prov commands.Provenance) {
		t.Helper()
		require.Equal(t, rep.Primary.Source, prov.Source,
			"%s: every row's provenance names the source the report was read from", what)
		require.Equal(t, rep.Primary.AgeMS, prov.AgeMS,
			"%s: every row is exactly as old as the observation it was read from", what)
		switch prov.Status {
		case commands.AvailabilityOK:
			require.NotNil(t, lat, "%s: an available row must carry the value it claims", what)
			require.Positive(t, lat.N, "%s: an available row was observed at least once", what)
			require.Empty(t, prov.Reason, "%s: an available row has nothing to explain", what)
		default:
			require.Nil(t, lat, "%s: a row nothing measured must not display a value", what)
			require.NotEmpty(t, prov.Reason, "%s: a row nothing measured must say why", what)
		}
	}
	for _, h := range rep.Hooks {
		check("hook "+h.Event, h.Latency, h.Provenance)
	}
	for _, b := range rep.Budgets {
		check("budget "+string(b.ID), b.Latency, b.Provenance)
	}
}

// x1v5RequireLatencyEquals asserts a displayed reading is the authoritative histogram, quantile
// for quantile, at the microsecond resolution the instrument actually has, under the one display
// rule status applies (e2e7e480, commands.latencyOf): a percentile is shown clamped to the
// snapshot's max when the max is known. obs reports a percentile as its bucket's upper bound, up to
// ~9% above every sample in that bucket, while Max is the largest sample itself, so an unclamped
// p99 above the max was a number no sample had. Every field is still compared exactly: N and Max
// as recorded, each percentile as recorded or, when it exceeds a known max, as that max.
func x1v5RequireLatencyEquals(t *testing.T, what string, got *commands.Latency, want obs.HistSnapshot) {
	t.Helper()
	shown := func(p time.Duration) int64 {
		if want.Max > 0 && p > want.Max {
			p = want.Max
		}
		return p.Microseconds()
	}
	require.NotNil(t, got, "%s: expected a displayed reading", what)
	require.Equal(t, want.N, got.N, "%s: sample count", what)
	require.Equal(t, shown(want.P50), got.P50US, "%s: p50", what)
	require.Equal(t, shown(want.P95), got.P95US, "%s: p95", what)
	require.Equal(t, shown(want.P99), got.P99US, "%s: p99", what)
	require.Equal(t, want.Max.Microseconds(), got.MaxUS, "%s: max", what)
}

// x1v5BudgetRow returns the row for id, which x1v5RunStatus has already proven to exist.
func x1v5BudgetRow(t *testing.T, rep commands.StatusReport, id obs.BudgetID) commands.BudgetRow {
	t.Helper()
	for _, b := range rep.Budgets {
		if b.ID == id {
			return b
		}
	}
	require.FailNowf(t, "budget row missing", "status has no row for %s", id)
	return commands.BudgetRow{}
}

// x1v5AdminDrain asks the daemon at root to run one synchronous Drain (ipc.OpAdminDrain), the
// transport-side equivalent of the v4 rig's in-process d.Drain. It is Eventually-safe: a daemon
// that cannot be reached is simply not drained on this poll.
func x1v5AdminDrain(root string) {
	addr, err := ipc.Resolve(root)
	if err != nil {
		return
	}
	sp, _ := ipc.NewSpool(paths.Of(root).Spool)
	c := ipc.NewClientWithOptions(addr, sp, nil, nil, ipc.ClientOptions{
		ProjectRoot:     root,
		ConnectDeadline: e2eRoundTripDeadline,
		AckDeadline:     e2eRoundTripDeadline,
	})
	defer func() { _ = c.Close() }()
	_, _ = c.Send(context.Background(), ipc.Request{
		Op: ipc.OpAdminDrain, TS: core.NowMilli(core.SystemClock()), Reply: true,
	}, e2eRoundTripDeadline)
}

// x1v5ClientSpools lists the client-*.ndjson fallback spools still on disk: each is a hook
// delivery the daemon has not replayed yet (see obsClientSpoolPrefix).
func x1v5ClientSpools(root string) []string {
	files, err := ipc.SpoolFiles(paths.Of(root).Spool)
	if err != nil {
		return nil
	}
	var out []string
	for _, f := range files {
		if strings.HasPrefix(filepath.Base(f), obsClientSpoolPrefix) {
			out = append(out, filepath.Base(f))
		}
	}
	return out
}

// x1v5LoudDiag renders the LOUD.log tail at FORMAT time, not at the call. require.Eventually
// evaluates its message arguments before the wait begins, so a loudLines(t, root) passed there
// would show the log as it was when the wait STARTED; fmt calls String when it builds the failure
// message, which is the state that explains the timeout (the same trick as obsWaitDiag).
type x1v5LoudDiag struct{ root string }

func (d x1v5LoudDiag) String() string {
	b, err := os.ReadFile(paths.Long(filepath.Join(paths.Of(d.root).Logs, "LOUD.log")))
	if os.IsNotExist(err) {
		return "(no LOUD.log: nothing was Loud-logged)"
	}
	if err != nil {
		return fmt.Sprintf("(LOUD.log unreadable: %v)", err)
	}
	return strings.TrimRight(string(b), "\n")
}

// x1v5TryStatus is e2eStatus without the assertions: one direct OpStatus read that reports
// failure instead of failing the test, so it can run inside an Eventually poll and inside a
// diagnostic Stringer, where a daemon that is momentarily unreachable is information, not an error.
func x1v5TryStatus(root string) (daemon.StatusSnapshot, error) {
	var snap daemon.StatusSnapshot
	addr, err := ipc.Resolve(root)
	if err != nil {
		return snap, err
	}
	sp, _ := ipc.NewSpool(paths.Of(root).Spool)
	c := ipc.NewClientWithOptions(addr, sp, nil, nil, ipc.ClientOptions{
		ProjectRoot:     root,
		ConnectDeadline: e2eRoundTripDeadline,
		AckDeadline:     e2eRoundTripDeadline,
	})
	defer func() { _ = c.Close() }()
	resp, err := c.Send(context.Background(), ipc.Request{
		Op: ipc.OpStatus, Session: e2eSession, TS: core.NowMilli(core.SystemClock()), Reply: true,
	}, e2eRoundTripDeadline)
	if err != nil {
		return snap, err
	}
	if !resp.OK {
		return snap, fmt.Errorf("status round trip refused: %s", resp.Err)
	}
	return snap, json.Unmarshal(resp.Data, &snap)
}

// x1v5PromptsLost is the daemon's own count of prompt captures whose verbatim store write failed
// (x1v5PromptPutErrCounter), or -1 when the daemon could not be asked.
func x1v5PromptsLost(root string) int64 {
	snap, err := x1v5TryStatus(root)
	if err != nil {
		return -1
	}
	return snap.Counters[x1v5PromptPutErrCounter]
}

// x1v5WaitDiag names, at FORMAT time, which mechanism the setup wait timed out on. The shared
// obsWaitDiag knows two: an undrained client spool (a NAKed tool/stop delivery waiting on Drain)
// and "every event reached the daemon live, so this waited on the processing behind its ACK". The
// prompt soft-drop is a third shape — no spool, no LOUD line, an index short by exactly the lost
// prompts — that only the daemon's own counters can tell apart from the second, so this renders
// an OpStatus read's observer.err.* and l0_accept_error counters and the hook_controlled /
// l0_ingest sample counts beside the index and spool facts, then the LOUD tail.
type x1v5WaitDiag struct {
	root           string
	hookControlled string
	l0Ingest       string
}

func (d x1v5WaitDiag) String() string {
	var b strings.Builder
	lines := len(obsToolUseLines(d.root))
	fmt.Fprintf(&b, "index holds %d lines (production promises %d; %d only if every prompt landed)",
		lines, x1v5DeliveryEvents, x1v5HotPathEvents)
	if pending := x1v5ClientSpools(d.root); len(pending) > 0 {
		fmt.Fprintf(&b, "; undrained client spool %v, so a tool/stop delivery is waiting on Drain", pending)
	} else {
		b.WriteString("; no undrained client spool")
	}
	snap, err := x1v5TryStatus(d.root)
	if err != nil {
		fmt.Fprintf(&b, "; daemon unreachable for a status read (%v)", err)
	} else {
		fmt.Fprintf(&b, "; daemon: %s.N=%d %s.N=%d %s=%d",
			d.hookControlled, snap.Latency[d.hookControlled].N,
			d.l0Ingest, snap.Latency[d.l0Ingest].N,
			x1v5L0AcceptErrCounter, snap.Counters[x1v5L0AcceptErrCounter])
		var soft []string
		for name, v := range snap.Counters {
			if strings.HasPrefix(name, x1v5ObserverErrPrefix) && v != 0 {
				soft = append(soft, fmt.Sprintf("%s=%d", name, v))
			}
		}
		if len(soft) == 0 {
			b.WriteString(" (no observer.err.* counter moved)")
		} else {
			fmt.Fprintf(&b, " %v", soft)
		}
		if lost := snap.Counters[x1v5PromptPutErrCounter]; lost > 0 && lines+int(lost) < x1v5HotPathEvents {
			fmt.Fprintf(&b, "; %d prompt(s) lost the reply deadline and %d record(s) are still unaccounted for",
				lost, x1v5HotPathEvents-lines-int(lost))
		} else if lost > 0 {
			fmt.Fprintf(&b, "; %d prompt(s) lost the reply deadline (soft-dropped, reconciled)", lost)
		}
	}
	fmt.Fprintf(&b, "; LOUD: %s", x1v5LoudDiag{d.root})
	return b.String()
}

// x1v5SessionRows decodes the daemon's own session rows out of the raw JSON status forwards.
func x1v5SessionRows(t *testing.T, raw []json.RawMessage) []daemon.SessionState {
	t.Helper()
	out := make([]daemon.SessionState, 0, len(raw))
	for _, r := range raw {
		var s daemon.SessionState
		require.NoError(t, json.Unmarshal(r, &s), "a session row must be the daemon's SessionState: %s", r)
		out = append(out, s)
	}
	return out
}

// TestV5_ObserveToStatusRoundTrip is V5-VERIFY §4.1.
//
// Arm 1 drives the historical event mix through the real hooks and reads status back from the
// live daemon, bracketing the command between two direct OpStatus reads so that "agrees with the
// authoritative observation" is an equality against the daemon's own payload and not against a
// number this test invented. Arms 2 and 3 are the negative controls, each through a real switch:
// the daemon is shut down (status must fall back to the snapshot the daemon persisted, and that
// snapshot must still agree with the live reading), and then the snapshot is removed (status must
// report that nothing was observed — every row unavailable, no value anywhere, exit still 0).
func TestV5_ObserveToStatusRoundTrip(t *testing.T) {
	bin := Build(t)
	p := testutil.NewProject(t)
	t.Cleanup(func() { e2eShutdownIfReachable(t, p.Root) })
	env := e2eEnv(p)

	hookControlled := x1v5HistOf(t, obs.BA)
	l0Ingest := x1v5HistOf(t, obs.BB)

	// ── The live half of a session, through the real binary ─────────────────────────────────────
	obsRunHook(t, bin, []string{"session-start"}, sessionStartFor(t, p.Root, x1v5Session), env)
	e2eWaitDaemonUp(t, p.Root)

	// Distinct ids, paths and content: no supersession, so the index line count is exact.
	for i := range x1v5ToolEvents {
		obsRunHook(t, bin, []string{"observe", "tool"},
			obsToolPayload(t, p.Root, x1v5Session, fmt.Sprintf("toolu_v5x01_%02d", i),
				fmt.Sprintf("src/v5x01_%02d.go", i),
				fmt.Sprintf("package v5x01\n\nfunc handler%02d() error { return nil }\n", i)), env)
	}
	for i := range x1v5PromptEvents {
		obsRunHook(t, bin, []string{"observe", "prompt"},
			obsPromptPayload(t, p.Root, x1v5Session, fmt.Sprintf("prompt number %d, keep going", i)), env)
	}
	obsRunHook(t, bin, []string{"observe", "stop", "--subagent"},
		obsSubagentStopPayload(t, p.Root, x1v5Session, "worker", "did the thing"), env)

	// Every tool and stop event is durable the moment its hook exits — live, or in the hook's own
	// client spool when the ACK did not arrive inside the hot-path deadline — but only the daemon's
	// Drain turns a spooled one into an index record, and its own tick is up to idleTickMax away.
	// Drive Drain through admin.drain on every poll, exactly as the v4 rig drives it in-process
	// (WaitIndexed), so the wait is on the observer's work and never on the tick.
	//
	// A prompt is the exception (x1v5PromptPutErrCounter): its capture runs in the worker behind
	// the ACK, and a capture whose verbatim store write fails is counted there rather than NAKed,
	// and is not recovered at all when its delivery was unleased. So the wait cannot be on 65 index
	// lines — on a loaded machine that is a wait on something that may never happen — and is
	// instead on: every tool/stop record landed, no client spool left, and every prompt accounted
	// for as either a record or a counted capture failure. Nothing is then in flight when the
	// authoritative reads below are taken.
	diag := x1v5WaitDiag{root: p.Root, hookControlled: hookControlled, l0Ingest: l0Ingest}
	require.Eventually(t, func() bool {
		x1v5AdminDrain(p.Root)
		lines := len(obsToolUseLines(p.Root))
		if lines < x1v5DeliveryEvents || len(x1v5ClientSpools(p.Root)) != 0 {
			return false
		}
		return int64(lines)+x1v5PromptsLost(p.Root) >= int64(x1v5HotPathEvents)
	}, obsProcessBound, obsProcessTick,
		"the %d tool/stop records never landed with the spool drained and every prompt accounted for: %s",
		x1v5DeliveryEvents, diag)

	// ── Arm 1: status from the live daemon, bracketed by the authoritative payload ───────────────
	before := e2eStatus(t, p.Root)

	// Reconcile the index against the daemon's own account exactly: with the drain finished, the
	// mix is partitioned into records and counted prompt drops with nothing left over. A record
	// can only be missing for a reason the daemon counted, and a counted drop can only be a
	// prompt (the tool/stop paths NAK and spool instead), so this equality is the criterion's
	// "authoritative observation" applied to the observer's own output.
	promptsLost := before.Counters[x1v5PromptPutErrCounter]
	require.LessOrEqual(t, promptsLost, int64(x1v5PromptEvents), "only a prompt can be soft-dropped, once each")
	require.Equal(t, int64(x1v5HotPathEvents)-promptsLost, int64(len(obsToolUseLines(p.Root))),
		"index records + counted prompt soft-drops must partition the mix: %s", diag)
	if promptsLost > 0 {
		t.Logf("V5 4.1 setup: %d of %d prompts lost the observe.prompt reply deadline and were "+
			"soft-dropped (counter %s); index holds %d records", promptsLost, x1v5PromptEvents,
			x1v5PromptPutErrCounter, len(obsToolUseLines(p.Root)))
	}
	rep := x1v5RunStatus(t, bin, p)
	after := e2eStatus(t, p.Root)

	t.Run("live", func(t *testing.T) {
		require.Equal(t, commands.SourceDaemon, rep.Primary.Source)
		require.Equal(t, commands.AvailabilityOK, rep.Primary.Status)
		require.NotNil(t, rep.Primary.AgeMS, "a live answer has a known age")
		require.GreaterOrEqual(t, *rep.Primary.AgeMS, int64(0))
		require.Empty(t, rep.Primary.Reason, "a live answer has nothing to explain")
		require.NotNil(t, rep.Snapshot, "a live answer forwards the daemon's payload")

		// The forwarded payload is the daemon's, member for member, against the two reads that
		// bracket it. Everything that can move between two reads only grows: a histogram is
		// never edited by a drain, a replay or an idle tick, and a counter is never decremented.
		// And a histogram is a pure function of its samples: when the sample count did not move
		// between the direct read and the forwarded one — the normal case, since the wait above
		// left nothing in flight and OpStatus is not itself a hot-path op — the two are the same
		// state and every quantile must be equal. Count-only agreement would let a status that
		// forwards the right N with the wrong quantiles pass this row (it did, once).
		require.Equal(t, contract.ModeFull.String(), before.Mode, "this session never degraded")
		require.Equal(t, before.Mode, rep.Snapshot.Mode)
		require.Equal(t, before.Hot, rep.Snapshot.Hot)
		require.Equal(t, before.Contract, rep.Snapshot.Contract)
		for name, hs := range before.Latency {
			got, ok := rep.Snapshot.Latency[name]
			require.True(t, ok, "histogram %q served directly must be forwarded by status", name)
			if got.N == hs.N {
				require.Equal(t, hs, got,
					"histogram %q: same sample count, so status must forward the daemon's own quantiles", name)
				continue
			}
			require.Greater(t, got.N, hs.N, "histogram %q only grows", name)
			require.LessOrEqual(t, got.N, after.Latency[name].N, "histogram %q only grows", name)
		}
		for name := range rep.Snapshot.Latency {
			_, ok := after.Latency[name]
			require.True(t, ok, "status forwarded histogram %q, which the daemon never served", name)
		}
		for name, v := range before.Counters {
			require.GreaterOrEqual(t, rep.Snapshot.Counters[name], v, "counter %q only grows", name)
			require.LessOrEqual(t, rep.Snapshot.Counters[name], after.Counters[name], "counter %q only grows", name)
		}

		// The live-delivery count. A hook that did not get its ACK inside the hot-path deadline
		// spools and exits 0 just the same, and a drained spool line never feeds a histogram, so the
		// count is not fixed by the event mix: it is however many deliveries the daemon took live —
		// at least one, or no daemon was ever reached, and at most the mix. What IS fixed is that the
		// three instruments recording a live delivery agree with each other to the sample: the B-A
		// estimate, the observed lower bound it is derived from, and B-B's ingest.Accept.
		hc := rep.Snapshot.Latency[hookControlled]
		require.Positive(t, hc.N, "no hot-path delivery reached the daemon live; LOUD: %s", x1v5LoudDiag{p.Root})
		require.LessOrEqual(t, hc.N, int64(x1v5HotPathEvents), "more samples than deliveries")
		require.Equal(t, hc.N, rep.Snapshot.Latency[x1v5HookControlledObserved].N,
			"the estimate and its observed lower bound are recorded from the same deliveries")
		require.Equal(t, hc.N, rep.Snapshot.Latency[l0Ingest].N,
			"every live delivery that was timed was also accepted into the WAL, and vice versa")

		// The registry saw THIS session, and status did not conjure a second one by asking.
		sessions := x1v5SessionRows(t, rep.Snapshot.Sessions)
		require.Len(t, sessions, len(before.Sessions), "a status read registers no session")
		var found bool
		for i, s := range sessions {
			if s.ID != x1v5Session {
				continue
			}
			found = true
			require.True(t, s.Live, "the session that sent the events is live")
			require.Equal(t, before.Sessions[i].Events, s.Events,
				"status forwards the daemon's own event count for the session")
			require.Equal(t, int(hc.N), s.Events,
				"the registry's own count of live deliveries for the session (registry.Touch runs on the "+
					"live routes only) is the histogram's sample count: two instruments, one number")
		}
		require.True(t, found, "the session that sent the events must be in the status payload: %+v", sessions)

		x1v5RequireQualified(t, rep)

		// B-A is the qualified view of hook_controlled: displayed, because it was observed;
		// marked ESTIMATED, because the daemon records it as the observed lower bound plus a fixed
		// tail allowance; marked AGGREGATE over the six hooks that deliver into it, because it
		// cannot be attributed to any one of them. The estimate is never below the measured lower
		// bound it was derived from.
		ba := x1v5BudgetRow(t, rep, obs.BA)
		require.Equal(t, commands.AvailabilityOK, ba.Provenance.Status)
		require.Equal(t, commands.MeasureEstimated, ba.Measure)
		require.True(t, ba.Aggregate, "B-A mixes every delivering hook")
		require.Equal(t, hookControlled, ba.Hist)
		x1v5RequireLatencyEquals(t, "B-A", ba.Latency, hc)
		if direct := before.Latency[hookControlled]; direct.N == hc.N {
			x1v5RequireLatencyEquals(t, "B-A against the direct read", ba.Latency, direct)
		}
		require.Equal(t, commands.MeasureEstimated, ba.Latency.Measure)
		observed := rep.Snapshot.Latency[x1v5HookControlledObserved]
		require.GreaterOrEqual(t, ba.Latency.P50US, observed.P50.Microseconds(), "estimate ≥ observed lower bound (p50)")
		require.GreaterOrEqual(t, ba.Latency.P99US, observed.P99.Microseconds(), "estimate ≥ observed lower bound (p99)")
		require.GreaterOrEqual(t, ba.Latency.MaxUS, observed.Max.Microseconds(), "estimate ≥ observed lower bound (max)")

		var baHooks []string
		for _, e := range pluginmanifest.HookEntryPoints() {
			if e.Event != "PreCompact" {
				baHooks = append(baHooks, e.Event)
			}
		}
		require.Equal(t, baHooks, ba.Covers, "B-A names exactly the hooks it mixes, in hooks.json order")

		// B-B is a measurement, one sample per accepted delivery, and not a hook budget at all.
		bb := x1v5BudgetRow(t, rep, obs.BB)
		require.Equal(t, commands.AvailabilityOK, bb.Provenance.Status)
		require.Equal(t, commands.MeasureObserved, bb.Measure)
		require.True(t, bb.Aggregate, "B-B is attributable to no single hook entry point")
		require.Empty(t, bb.Covers)
		x1v5RequireLatencyEquals(t, "B-B", bb.Latency, rep.Snapshot.Latency[l0Ingest])
		if direct := before.Latency[l0Ingest]; direct.N == rep.Snapshot.Latency[l0Ingest].N {
			x1v5RequireLatencyEquals(t, "B-B against the direct read", bb.Latency, direct)
		}

		// The figures the V5 report's §8.4 row cites (tool_uses=, p99=), from this run's artifact.
		t.Logf("V5 4.1 live: index_tool_uses=%d hook_controlled.N=%d l0_ingest.N=%d "+
			"B-A p99=%dus (estimated) B-B p99=%dus (observed)",
			len(obsToolUseLines(p.Root)), hc.N, rep.Snapshot.Latency[l0Ingest].N, ba.Latency.P99US, bb.Latency.P99US)

		// The measured lower bound is served in the snapshot but is nobody's budget row: SP-14
		// keeps it beside B-A rather than presenting it as a second B-A.
		for _, b := range rep.Budgets {
			require.NotEqual(t, x1v5HookControlledObserved, b.Hist,
				"%s must not present the observed lower bound as a budget reading", b.ID)
		}

		// Every other budget row agrees with the authoritative payload on whether it was observed
		// at all, and B-D is unavailable no matter what: nothing in this tree can see host process
		// creation, and an empty histogram would be the wrong statement for it.
		for _, b := range rep.Budgets {
			hs, served := after.Latency[b.Hist]
			switch {
			case b.ID == obs.BD:
				require.Equal(t, commands.AvailabilityUnavailable, b.Provenance.Status,
					"B-D is uninstrumented by construction")
				require.Nil(t, b.Latency)
			case served && hs.N > 0 && before.Latency[b.Hist].N > 0:
				require.Equal(t, commands.AvailabilityOK, b.Provenance.Status,
					"%s: the daemon served %d samples for %q, status must display them", b.ID, hs.N, b.Hist)
			case !served || hs.N == 0:
				require.Equal(t, commands.AvailabilityUnavailable, b.Provenance.Status,
					"%s: nothing observed %q, status must not display a value for it", b.ID, b.Hist)
				require.Contains(t, b.Provenance.Reason, b.Hist,
					"%s: the reason names the histogram nothing recorded into", b.ID)
			}
		}

		// Hook rows: the seven installed entry points, in hooks.json order, and only PreCompact
		// has an instrument of its own. The other six are folded into B-A and say so — this is
		// the "missing telemetry" half of the criterion, and the historical per-hook
		// `observe_tool` row is precisely the claim it retires.
		for i, e := range pluginmanifest.HookEntryPoints() {
			row := rep.Hooks[i]
			require.Equal(t, e.Event, row.Event)
			require.Equal(t, e.Subcommand, row.Subcommand)
			require.Equal(t, e.TimeoutSeconds, row.TimeoutSeconds)
			require.NotEmpty(t, row.Budget, "%s: every hook is covered by a §2.4 budget", e.Event)
			require.Equal(t, commands.AvailabilityUnavailable, row.Provenance.Status,
				"%s: no hook was measured alone in this session", e.Event)
			require.Nil(t, row.Latency)
			if e.Event == "PreCompact" {
				require.Equal(t, string(obs.BE), row.Budget)
				require.Contains(t, row.Provenance.Reason, x1v5HistOf(t, obs.BE),
					"PreCompact has its own instrument, and it recorded nothing: the reason names it")
				continue
			}
			require.Equal(t, string(obs.BA), row.Budget)
			require.Contains(t, row.Provenance.Reason, hookControlled,
				"%s: the reason names the aggregate this hook is folded into", e.Event)
		}
	})

	// ── Arm 2 (negative control): the daemon shut down, status answers from what it persisted ───
	//
	// The switch is real: admin.shutdown through the transport, awaited until the process is gone.
	// Stop persists the metrics registry to metrics/latency.json as one of its last acts, so the
	// only observation left is that file — and it must still agree with the live reading, or one
	// of the two sources was lying.
	e2eShutdownIfReachable(t, p.Root)
	persistedPath := paths.Long(filepath.Join(paths.Of(p.Root).Metrics, x1v5LatencyFile))

	t.Run("daemon_down_falls_back_to_persisted_snapshot", func(t *testing.T) {
		raw, err := os.ReadFile(persistedPath)
		require.NoError(t, err, "the daemon persists its metrics on Stop")
		var persisted obs.Snapshot
		require.NoError(t, json.Unmarshal(raw, &persisted))
		// Stop persists after its own final Drain, so the file holds at least what the last live
		// read saw — and, since a drained line never feeds a histogram, never more than the mix.
		// And by the same rule the live arm applies at its direct read: when the sample count did
		// not move between the last live read and the persisted file (the normal case — nothing
		// was in flight and status is not a hot-path op), the persisted histogram IS the served one,
		// every quantile of it; a Persist that wrote the right count with the wrong quantiles must
		// not pass. Only when N moved is the band accepted instead.
		requirePersistedAgrees := func(name string) obs.HistSnapshot {
			t.Helper()
			served := after.Latency[name]
			got := persisted.Hists[name]
			if got.N == served.N {
				require.Equal(t, served, got,
					"histogram %q: same sample count, so the persisted snapshot must be the served one", name)
				return got
			}
			require.Greater(t, got.N, served.N, "what the daemon persisted must hold everything it had served (%q)", name)
			require.LessOrEqual(t, got.N, int64(x1v5HotPathEvents), "histogram %q: more samples than deliveries", name)
			return got
		}
		persistedHC := requirePersistedAgrees(hookControlled)
		persistedL0 := requirePersistedAgrees(l0Ingest)

		down := x1v5RunStatus(t, bin, p)
		require.Equal(t, commands.SourceDisk, down.Primary.Source)
		require.Equal(t, commands.AvailabilityOK, down.Primary.Status)
		require.NotNil(t, down.Primary.AgeMS, "a persisted snapshot carries its own timestamp")
		require.GreaterOrEqual(t, *down.Primary.AgeMS, int64(0))
		require.Contains(t, down.Primary.Reason, "daemon:",
			"a fallback says why the preferred source did not answer")
		require.Nil(t, down.Snapshot, "there is no live payload to forward, and none is invented")

		x1v5RequireQualified(t, down)

		// The disk reading is the persisted histogram to the quantile, and it is the same
		// distribution the live arm displayed: same sample count band, same estimate qualifier.
		ba := x1v5BudgetRow(t, down, obs.BA)
		x1v5RequireLatencyEquals(t, "B-A from disk", ba.Latency, persistedHC)
		require.Equal(t, commands.MeasureEstimated, ba.Latency.Measure)
		require.GreaterOrEqual(t, ba.Latency.N, rep.Snapshot.Latency[hookControlled].N,
			"the persisted B-A holds every sample the live arm displayed")
		bb := x1v5BudgetRow(t, down, obs.BB)
		x1v5RequireLatencyEquals(t, "B-B from disk", bb.Latency, persistedL0)
		require.Equal(t, ba.Latency.N, bb.Latency.N, "B-A and B-B still agree on the delivery count from disk")
		for i, b := range down.Budgets {
			require.Equal(t, rep.Budgets[i].ID, b.ID)
			if b.ID == obs.BD {
				require.Equal(t, commands.AvailabilityUnavailable, b.Provenance.Status)
				continue
			}
			require.Equal(t, rep.Budgets[i].Provenance.Status, b.Provenance.Status,
				"%s: the two sources agree on whether it was observed", b.ID)
		}
		for _, h := range down.Hooks {
			require.Equal(t, commands.AvailabilityUnavailable, h.Provenance.Status)
		}
	})

	// A status command that finds no daemon lazily spawns one, exactly as a hook would — it is the
	// same seam `qompack mcp` uses (internal/cli/qompack_commands.go newCommandClient). That daemon
	// is a fresh process with an empty registry; it must be up and gone again before the last arm
	// removes the file, or it could answer the arm live or re-persist the file underneath it.
	e2eWaitDaemonUp(t, p.Root)
	e2eShutdownIfReachable(t, p.Root)

	// ── Arm 3 (negative control): nothing left to observe ────────────────────────────────────────
	require.NoError(t, os.Remove(persistedPath), "removing the persisted snapshot")
	require.NoFileExists(t, persistedPath)

	t.Run("nothing_observed_is_reported_as_unknown", func(t *testing.T) {
		none := x1v5RunStatus(t, bin, p)
		require.Equal(t, commands.SourceNone, none.Primary.Source)
		require.Equal(t, commands.AvailabilityError, none.Primary.Status,
			"a source that was configured and could not be read is an error, not a quiet absence")
		require.Nil(t, none.Primary.AgeMS, "an unknown age serializes as null, never as zero")
		require.Contains(t, none.Primary.Reason, "daemon:")
		require.Contains(t, none.Primary.Reason, "disk:")
		require.Nil(t, none.Snapshot)

		x1v5RequireQualified(t, none)
		for _, h := range none.Hooks {
			require.Equal(t, commands.AvailabilityUnavailable, h.Provenance.Status)
			require.Nil(t, h.Latency)
		}
		for _, b := range none.Budgets {
			require.Equal(t, commands.AvailabilityUnavailable, b.Provenance.Status,
				"%s: with nothing reachable, no row may display a value", b.ID)
			require.Nil(t, b.Latency)
			if b.ID != obs.BD {
				require.Equal(t, none.Primary.Reason, b.Provenance.Reason,
					"%s: the row's reason is the report's own — nothing answered, so no histogram was empty", b.ID)
			}
		}
	})

	p.AssertAppendOnly(t)
}

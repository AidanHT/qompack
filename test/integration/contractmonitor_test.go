// Contract monitor against a real store — plans/V2-VERIFY-primitives-store-dag-and-baseline.md
// §4.3. internal/contract may import store (§3.2); on SP-05's branch the Env.Store it ran against
// was the stub. These two tests are the first execution of the §12.1 monitor with a real L1 behind
// it: RunAll observing real transcript/payload data with a real store in the Env, and the
// degraded-passive doctrine ("L0 and L1 keep running … the store stays correct and the session's
// data is not lost") proven against the actual store rather than a test double.
package integration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/contract"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/daemon"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/obs"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/store"
	"github.com/qompack/qompack/internal/testutil"
)

const (
	// standardAssertionCount is the size of contract.StandardAssertions(): the nine host-contract
	// assertions of 00-ARCHITECTURE.md §5.19. Pinned so "exactly four not-yet-implemented" below
	// is measured against a known total, not a drifting one.
	standardAssertionCount = 9

	// notYetImplementedObserved is the exact Observed string §12.1 gives an assertion whose
	// producer is absent from the build. internal/contract freezes it (standard.go); this is the
	// spelling this test counts.
	notYetImplementedObserved = "not-yet-implemented"

	// notImplementedMarker is the telltale substring of core.ErrNotImplemented's message
	// ("qompack: not implemented"). §4.3: no assertion may touch a store method that still
	// returns the stub error, asserted by failing on any Result whose text carries it. The four
	// declared not-yet-implemented results are spelled with hyphens and never match this
	// space-separated form, so every result can be scanned with no exception list.
	notImplementedMarker = "not implemented"

	// seedPayloadCount is how many real tool results test 1 ingests before RunAll, so the store
	// the assertions see is a live L1 with content, not an empty directory.
	seedPayloadCount = 3

	// degradedEventCount is §4.3's "drive 20 observe.tool events" count.
	degradedEventCount = 20

	// probeDialTimeout bounds one ipc.Probe dial while waiting for the daemon to accept.
	probeDialTimeout = 250 * time.Millisecond

	// daemonUpWait / daemonUpTick bound and pace the wait for the in-process daemon to start
	// serving. Generous on purpose: the machine may be under parallel load, and this is a bound,
	// never a latency assertion.
	daemonUpWait = 30 * time.Second
	daemonUpTick = 20 * time.Millisecond

	// storeSettleWait / storeSettleTick bound and pace the wait for the asynchronous ingest
	// worker pool to land every observed event in the store.
	storeSettleWait = 30 * time.Second
	storeSettleTick = 25 * time.Millisecond

	// statusReplyDeadline bounds the status round trip this file's own ipc.Client performs —
	// every step of it, not only the reply read: statusMode hands it to ClientOptions.
	// ConnectDeadline and ClientOptions.AckDeadline as well as to Send. See statusMode's own
	// comment for why leaving the dial on state.bin's budget was a real, Windows-only failure.
	statusReplyDeadline = 5 * time.Second
)

// The two tests' session ids, distinct so a failure message names its test unambiguously.
const (
	monitorSession  = core.SessionID("sess-contract-monitor")
	degradedSession = core.SessionID("sess-degraded-passive")
)

// openRealStore opens the project's store with EVERY collaborator real — the exact §4.1/§4.3
// dependency set (chunk.New(chunk.FromConfig), canon.Default, symbols.New, tokens.NewExact,
// redact.New), none nil, none faked — rather than store.Open's nil-filled defaults, so the test
// states explicitly that nothing behind the Store seam is a double.
// writeRealTranscript writes a real JSONL transcript whose last non-empty line is valid JSON —
// what transcript.readable actually probes — and returns its path.
func writeRealTranscript(t *testing.T, p *testutil.Project) string {
	t.Helper()
	path := filepath.Join(p.Root, "transcript.jsonl")
	lines := `{"type":"user","message":{"role":"user","content":"read the auth module"}}` + "\n" +
		`{"type":"assistant","message":{"role":"assistant","content":"reading src/auth.ts"}}` + "\n"
	require.NoError(t, os.WriteFile(path, []byte(lines), 0o600))
	return path
}

// resultByID returns the one Result carrying id, failing the test if it is absent.
func resultByID(t *testing.T, results []contract.Result, id contract.ID) contract.Result {
	t.Helper()
	for _, r := range results {
		if r.ID == id {
			return r
		}
	}
	t.Fatalf("no result for assertion %s in %+v", id, results)
	return contract.Result{}
}

// TestIntegration_ContractMonitorRunsAgainstRealStore is §4.3's first test: the real
// contract.NewMonitor state machine, contract.StandardAssertions() registered, RunAll executed
// with an Env whose Store is the real L1 and whose Event carries real transcript/payload data.
//
// A wave-1 build declares exactly the five producers daemon.DeclareProducers names
// unconditionally, so exactly the four later-wave assertions (SP-10/SP-11/SP-13's) must report
// OK/SevInfo/not-yet-implemented, the mode must be ModeFull, and no assertion may have touched a
// store method still returning core.ErrNotImplemented.
func TestIntegration_ContractMonitorRunsAgainstRealStore(t *testing.T) {
	// The producer set is process-wide (contract/producers.go): establish the wave-1 baseline
	// regardless of what ran earlier in this binary, and undo it for whatever runs after. This
	// test is deliberately not parallel — it depends on that process-wide state.
	contract.ResetProducers()
	t.Cleanup(contract.ResetProducers)
	// plugin.root_resolves reads CLAUDE_PLUGIN_ROOT from the process environment; pin it empty so
	// a developer machine with a real plugin install still observes the deterministic "unset" arm.
	t.Setenv("CLAUDE_PLUGIN_ROOT", "")

	p := testutil.NewProject(t)
	ctx := context.Background()
	s := openRealStore(t, p)

	// Seed the store with real content through the real pipeline, so Env.Store is a live L1.
	for i := 0; i < seedPayloadCount; i++ {
		path := fmt.Sprintf("src/service%02d.ts", i)
		payload := []byte(fmt.Sprintf(
			"// tool result %d\nexport function handler%d(req: Request): Response {\n"+
				"  return new Response(req.headers.get(\"x-request-id\"));\n}\n", i, i))
		res, err := s.PutBytes(ctx, payload, store.PutOptions{Tool: "FileRead", Path: path})
		require.NoError(t, err)
		now := core.NowMilli(p.Clock)
		turn := core.TurnIndex(i)
		require.NoError(t, s.RecordToolUse(ctx, store.ToolUseRecord{
			ID: core.ToolUseID(fmt.Sprintf("toolu_monitor_seed_%02d", i)), Session: monitorSession,
			Turn: turn, TS: now, Tool: "FileRead", Root: res.Root.Hash, Path: path,
			Bytes: res.Root.RawBytes, Tokens: res.Root.Tokens,
		}))
		require.NoError(t, s.AppendFileVersion(ctx, path, store.FileVersion{
			TS: now, Root: res.Root.Hash, Turn: turn, Bytes: res.Root.RawBytes,
		}))
		p.Clock.Advance(time.Millisecond)
	}
	require.NoError(t, s.Flush(ctx))

	// Declare exactly what a wave-1 daemon build declares: DeclareProducers against a zero
	// Services (the internal/cli/selftest.go precedent) names the five SP-05 producers and none
	// of the four later-wave ones.
	daemon.DeclareProducers(&daemon.Services{})

	mon := contract.NewMonitor(p.Log, obs.New(p.Clock), filepath.Join(paths.Of(p.Root).State, "contract.json"))
	for _, a := range contract.StandardAssertions() {
		require.NoError(t, mon.Register(a))
	}

	transcript := writeRealTranscript(t, p)
	env := contract.Env{
		ProjectRoot: p.Root,
		Event: hookio.Event{
			HookEventName:  "SessionStart",
			SessionID:      monitorSession,
			TranscriptPath: transcript,
			CWD:            p.Root,
			Source:         "startup",
		},
		Cfg:     p.Cfg,
		Store:   s,
		Log:     p.Log,
		Clock:   p.Clock,
		History: contract.LoadHistory(contract.HistoryPath(p.Root)),
	}

	results, mode := mon.RunAll(ctx, env)

	require.Equal(t, contract.ModeFull, mode, "RunAll on a wave-1 build over a real store must report ModeFull")
	require.Equal(t, contract.ModeFull, mon.Mode())
	require.Len(t, results, standardAssertionCount)

	// Exactly four results at SevInfo/"not-yet-implemented" — exactly the four §12.1 names as
	// later-wave, no more and no fewer — and every result clean.
	var nyi []contract.ID
	for _, r := range results {
		require.True(t, r.OK, "assertion %s failed: %+v", r.ID, r)
		if r.Observed == notYetImplementedObserved {
			require.Equal(t, contract.SevInfo, r.Severity,
				"not-yet-implemented result %s must be SevInfo", r.ID)
			nyi = append(nyi, r.ID)
		}
	}
	require.ElementsMatch(t, []contract.ID{
		contract.CAdditionalContext,
		contract.CPreCompactTiming,
		contract.CPreCompactCustomInstr,
		contract.CMCPRegistered,
	}, nyi, "exactly the four later-wave assertions must be not-yet-implemented")

	// transcript.readable and hook.payload_shape evaluated against real data: their real Checks
	// ran (not the gate) and observed the real transcript file and the real event payload.
	tr := resultByID(t, results, contract.CTranscriptReadable)
	require.Equal(t, "transcript readable", tr.Observed,
		"transcript.readable must have parsed the real transcript, not skipped it")
	ps := resultByID(t, results, contract.CHookPayloadShape)
	require.Equal(t, "payload shape valid", ps.Observed,
		"hook.payload_shape must have validated the real event payload")

	// No assertion touched a store method returning core.ErrNotImplemented: no result may carry
	// the stub error's text anywhere it could surface.
	for _, r := range results {
		require.NotContains(t, r.Detail, notImplementedMarker,
			"assertion %s touched a not-implemented store method", r.ID)
		require.NotContains(t, r.Observed, notImplementedMarker,
			"assertion %s touched a not-implemented store method", r.ID)
	}
}

// contractStatePath returns the monitor's persisted state file for root — the same
// <root>/.qompack/state/contract.json every real caller hands contract.NewMonitor.
func contractStatePath(root string) string {
	return filepath.Join(paths.Of(root).State, "contract.json")
}

// forceDegradedPassive persists a degraded-passive monitor state before the daemon is
// constructed. §12.1 requires a degradation to survive into the next SessionStart, and
// contract.NewMonitor reads this file back at construction — so this forces the daemon's own
// monitor from outside internal/daemon through exactly the persistence seam a real degraded
// session uses, rather than through any test-only hook.
func forceDegradedPassive(t *testing.T, p *testutil.Project) {
	t.Helper()
	st := struct {
		Mode      string         `json:"mode"`
		Reason    string         `json:"reason,omitempty"`
		Since     core.UnixMilli `json:"since"`
		CleanRuns int            `json:"cleanRuns"`
	}{
		Mode:   contract.ModeDegradedPassive.String(),
		Reason: "forced by TestIntegration_DegradedPassiveStillWritesToTheRealStore",
		Since:  core.NowMilli(p.Clock),
	}
	b, err := json.Marshal(st)
	require.NoError(t, err)
	require.NoError(t, paths.WriteAtomic(contractStatePath(p.Root), b, 0o600))
}

// degradedEventPath / degradedToolUseID / degradedPayloadMarker name the i-th observe.tool
// event's file path, tool_use_id and distinctive payload substring. Distinct content per event is
// load-bearing twice over: the ingest dedup key is a hash of the encoded request line, and 20
// distinct payloads must produce 20 distinct roots in the content-addressed store.
func degradedEventPath(i int) string { return fmt.Sprintf("src/degraded/file%02d.ts", i) }

func degradedToolUseID(i int) core.ToolUseID {
	return core.ToolUseID(fmt.Sprintf("toolu_degraded_%02d", i))
}

func degradedPayloadMarker(i int) string { return fmt.Sprintf("degraded-payload-%02d", i) }

// statusMode asks the running daemon for its StatusSnapshot over the real ipc.Client and returns
// the contract mode it reports.
//
// status is NOT an ipc.Op.HotPath() op, so its client must not run on the hot path's budgets.
// Leaving ConnectDeadline/AckDeadline at zero makes NewClientWithOptions fall back to state.bin's
// own ConnectDeadlineMs/AckDeadlineMs out of config.Defaults() — 8ms for the ACK, and a connect
// budget sized for observe.tool/prompt/stop against an already-warm daemon — and the dial this
// helper actually performs is not that dial: it lands moments after the caller's own ipc.Probe
// accepted-and-closed a connection, and go-winio's listenerRoutine only creates the next pipe
// instance when its accept loop asks for one (pipe.go listenerRoutine), so a dial landing in that
// gap gets ERROR_PIPE_BUSY — which go-winio retries on a hard-coded 10ms sleep (pipe.go
// tryDialPipe). Measured on Windows 11 / go-winio v0.6.2: 197 of 200 dials issued straight after a
// Probe failed at a 5ms budget (worst elapsed 12.1ms — the 10ms sleep), 0 of 200 failed at 250ms.
// The client then spools and returns Response{OK:false} with an empty Err (ipc client.go
// spoolAndReturn), which reads exactly like a daemon refusal but is not one: the daemon was
// accepting the whole time.
//
// The 5ms in that measurement is the old Windows default; config now ships 25ms there, above the
// 12.1ms worst case it recorded (internal/config/deadlines.go). That closes the gap between "the
// hot-path budget" and "one busy-retry", but it does not make the hot-path budget the right one
// for this helper, which is off the hot path by construction and should not inherit a budget tuned
// for a warm daemon at all.
//
// This is the same condition internal/cli covers with hookConnectDeadlineFloor for every
// non-hot-path reply op (hookclient.go) and explicitly for admin.ping (selftest.go), and that
// this tree's other reply-op client constructors already widen for themselves: hotpathNewClient
// (hotpath_test.go), hookflow_test.go, appendonly_test.go. statusReplyDeadline is this file's own
// declared budget for the whole round trip, so every step of the round trip gets it.
func statusMode(t *testing.T, ctx context.Context, p *testutil.Project) string {
	t.Helper()
	addr, err := ipc.Resolve(p.Root)
	require.NoError(t, err)
	sp, err := ipc.NewSpool(paths.Of(p.Root).Spool)
	require.NoError(t, err)
	c := ipc.NewClientWithOptions(addr, sp, nil, nil, ipc.ClientOptions{
		ProjectRoot:     p.Root,
		ConnectDeadline: statusReplyDeadline,
		AckDeadline:     statusReplyDeadline,
	})
	defer func() { _ = c.Close() }()

	resp, err := c.Send(ctx, ipc.Request{
		Op: ipc.OpStatus, Session: degradedSession, Reply: true, TS: core.NowMilli(p.Clock),
	}, statusReplyDeadline)
	require.NoError(t, err)
	// Every daemon-side OK:false a status Reply can produce carries a reason — handleStatus's
	// marshal failure, dispatchOp's unknown-op arm, callHandler's/ipc dispatch's panic arms all
	// set Err — so an OK:false with an EMPTY Err is the ipc client's own spoolAndReturn: the
	// request never reached the daemon at all. Name that here so the next reader of this failure
	// does not have to re-derive it from the transport.
	require.True(t, resp.OK,
		"status must answer (empty Err = the client never reached the daemon and spooled instead): %+v", resp)
	var snap daemon.StatusSnapshot
	require.NoError(t, json.Unmarshal(resp.Data, &snap))
	return snap.Mode
}

// TestIntegration_DegradedPassiveStillWritesToTheRealStore is §4.3's second test — §12.1's "L0
// and L1 keep running … the store stays correct and the session's data is not lost", verified
// against the actual L1 rather than a stub.
//
// The monitor is forced to ModeDegradedPassive (persisted state, loaded by the daemon's own
// contract.NewMonitor at construction). A real daemon runs with an ObserveTool bound through the
// daemon.Options.Bind seam that performs store.PutBytes → store.RecordToolUse →
// store.AppendFileVersion against the real store. 20 observe.tool events driven through the real
// hook client must all land in the store (roots, tool-use records, file versions), while every
// acting seam stays suppressed: no hook's Output may carry a HookSpecificOutput.
//
// ObservePrompt is the one seam that sits on both sides of that line, and the assertions below
// treat it accordingly. §12.1 keeps "verbatim capture" running under ModeDegradedPassive, and the
// capture is what the ObservePrompt seam DOES, so it must still be CALLED; what must not happen is
// its returned Output reaching the reply. See handleObservePrompt's own doc comment for the split.
func TestIntegration_DegradedPassiveStillWritesToTheRealStore(t *testing.T) {
	// Not parallel: daemon.New declares producers into the process-wide set (and NewProject uses
	// t.Setenv). Reset on both sides so this test neither inherits nor leaks producer state.
	contract.ResetProducers()
	t.Cleanup(contract.ResetProducers)

	p := testutil.NewProject(t)
	ctx := context.Background()
	s := openRealStore(t, p)
	transcript := writeRealTranscript(t, p)

	forceDegradedPassive(t, p)

	// Seams that WOULD emit HookSpecificOutput if the daemon (wrongly) delivered while degraded.
	// Binding them makes the "no HookSpecificOutput on any hook" assertion below proof of
	// suppression rather than a vacuous truth about unbound seams.
	//
	// promptRan is named differently from the other two on purpose: SessionStart and PreCompact are
	// purely acting seams and must not run at all, while ObservePrompt also carries the recording
	// half §12.1 keeps alive, so running is exactly what it must do.
	var sessionStartActed, preCompactActed, promptRan atomic.Bool
	var turn atomic.Int64

	opts := daemon.NewOptions(p.Root, p.Cfg)
	opts.Log = p.Log
	opts.Clock = p.Clock
	opts.Store = s
	opts.Bind(func(sv *daemon.Services) {
		// The §4.2-shaped test-local binding through the seam SP-05 shipped for exactly this
		// purpose; replaced by observer.OnToolUse in V3. It performs the three L1 writes §4.3
		// names, in order, against the real store.
		sv.ObserveTool = func(ctx context.Context, e hookio.Event) error {
			var in struct {
				FilePath string `json:"file_path"`
			}
			if err := json.Unmarshal(e.ToolInput, &in); err != nil {
				return fmt.Errorf("integration: decoding tool_input: %w", err)
			}
			if in.FilePath == "" {
				return errors.New("integration: observe.tool event carries no file_path")
			}
			res, err := s.PutBytes(ctx, e.ToolResponse, store.PutOptions{Tool: e.ToolName, Path: in.FilePath})
			if err != nil {
				return err
			}
			now := core.NowMilli(p.Clock)
			tn := core.TurnIndex(turn.Add(1))
			if err := s.RecordToolUse(ctx, store.ToolUseRecord{
				ID: e.ToolUseID, Session: e.SessionID, Turn: tn, TS: now, Tool: e.ToolName,
				Root: res.Root.Hash, Path: in.FilePath, Bytes: res.Root.RawBytes, Tokens: res.Root.Tokens,
			}); err != nil {
				return err
			}
			return s.AppendFileVersion(ctx, in.FilePath, store.FileVersion{
				TS: now, Root: res.Root.Hash, Turn: tn, Bytes: res.Root.RawBytes,
			})
		}
		sv.SessionStart = func(context.Context, hookio.Event) (hookio.Output, error) {
			sessionStartActed.Store(true)
			return hookio.SessionStartOutput("must-never-be-delivered"), nil
		}
		sv.PreCompact = func(context.Context, hookio.Event) (hookio.Output, error) {
			preCompactActed.Store(true)
			return hookio.PreCompactOutput("must-never-be-delivered"), nil
		}
		sv.ObservePrompt = func(context.Context, hookio.Event) (hookio.Output, error) {
			promptRan.Store(true)
			return hookio.Output{HookSpecificOutput: &hookio.HSO{
				HookEventName: "UserPromptSubmit", AdditionalContext: "must-never-be-delivered",
			}}, nil
		}
	})

	d, err := daemon.New(opts)
	require.NoError(t, err)

	runCtx, cancel := context.WithCancel(ctx)
	runErr := make(chan error, 1)
	go func() { runErr <- d.Run(runCtx) }()
	// Registered after openRealStore's own cleanup so (LIFO) the daemon is fully stopped —
	// spool/WAL handles closed, lock released — before the store closes underneath it.
	t.Cleanup(func() {
		cancel()
		require.NoError(t, <-runErr, "daemon Run must shut down cleanly")
	})

	addr, err := ipc.Resolve(p.Root)
	require.NoError(t, err)
	require.Eventually(t, func() bool { return ipc.Probe(addr, probeDialTimeout) },
		daemonUpWait, daemonUpTick, "daemon never came up on %v", addr)

	// The persisted force took: the daemon's monitor is degraded before anything else happens.
	require.Equal(t, contract.ModeDegradedPassive.String(), statusMode(t, ctx, p),
		"the persisted degraded state must survive into the daemon's own monitor")

	hookOutputs := map[string]hookio.Output{}

	// SessionStart runs RunAll before any other work. One clean run must not restore (§12.1
	// needs two), and the acting half — sentinel injection, the bound SessionStart seam — must
	// stay off.
	hookOutputs["SessionStart"] = p.RunHook(t, "SessionStart", hookio.Event{
		HookEventName: "SessionStart", SessionID: degradedSession, CWD: p.Root,
		TranscriptPath: transcript, Source: "startup",
	})

	// The 20 observe.tool events, each distinct in path, tool_use_id and payload.
	for i := 0; i < degradedEventCount; i++ {
		body, merr := json.Marshal(map[string]string{"content": fmt.Sprintf(
			"// %s\nexport const marker%d = %q;\n",
			degradedPayloadMarker(i), i, degradedPayloadMarker(i))})
		require.NoError(t, merr)
		out := p.RunHook(t, "PostToolUse", hookio.Event{
			HookEventName: "PostToolUse", SessionID: degradedSession, CWD: p.Root,
			TranscriptPath: transcript, ToolName: "FileRead", ToolUseID: degradedToolUseID(i),
			ToolInput:    json.RawMessage(fmt.Sprintf(`{"file_path":%q}`, degradedEventPath(i))),
			ToolResponse: json.RawMessage(body),
		})
		hookOutputs[fmt.Sprintf("PostToolUse[%02d]", i)] = out
		p.Clock.Advance(time.Millisecond)
	}

	// Three more of §7.3's hooks. SessionEnd (flush) is deliberately NOT among them yet: its
	// route runs Drain, which replays the session's WAL, and exactly-once across a drain is
	// §4.2's dedup-rule territory, not §4.3's — sequencing flush after the read-backs below
	// keeps this test pinned to §4.3's own claims (every event lands, nothing acts)
	// independent of it. Authoring this test found that dedup broken — Accept keyed the
	// seen-set over the terminator-carrying line while drainFile keyed the trimmed one —
	// which ingest.Accept's trim now fixes, pinned by
	// TestLiveDispatchedLineIsNotRedispatchedByDrain in internal/daemon.
	hookOutputs["UserPromptSubmit"] = p.RunHook(t, "UserPromptSubmit", hookio.Event{
		HookEventName: "UserPromptSubmit", SessionID: degradedSession, CWD: p.Root,
		TranscriptPath: transcript, Prompt: "keep going",
	})
	hookOutputs["Stop"] = p.RunHook(t, "Stop", hookio.Event{
		HookEventName: "Stop", SessionID: degradedSession, CWD: p.Root, TranscriptPath: transcript,
	})
	hookOutputs["PreCompact"] = p.RunHook(t, "PreCompact", hookio.Event{
		HookEventName: "PreCompact", SessionID: degradedSession, CWD: p.Root,
		TranscriptPath: transcript, Trigger: "auto",
	})

	// All 20 land in the real store: the worker pool is asynchronous, so wait for the count —
	// a bound on eventual arrival, never a latency assertion.
	require.Eventually(t, func() bool {
		st, serr := s.Stats(ctx)
		return serr == nil && st.ToolUses >= degradedEventCount
	}, storeSettleWait, storeSettleTick, "the 20 observe.tool events never all reached the store")

	st, err := s.Stats(ctx)
	require.NoError(t, err)
	require.Equal(t, degradedEventCount, st.ToolUses,
		"exactly the 20 events must be recorded — no loss and no duplicate dispatch")

	// Roots, tool-use records and file versions, per event, read back from the real store.
	roots := map[core.Hash]bool{}
	for i := 0; i < degradedEventCount; i++ {
		rec, terr := s.ToolUse(ctx, degradedToolUseID(i))
		require.NoError(t, terr, "tool-use record %02d must be in the store", i)
		require.Equal(t, degradedEventPath(i), rec.Path)
		require.Equal(t, degradedSession, rec.Session)

		_, gerr := s.GetRoot(ctx, rec.Root)
		require.NoError(t, gerr, "root for event %02d must be in the store", i)
		rc, oerr := s.Open(ctx, rec.Root)
		require.NoError(t, oerr)
		content, rerr := io.ReadAll(rc)
		require.NoError(t, rc.Close())
		require.NoError(t, rerr)
		require.Contains(t, string(content), degradedPayloadMarker(i),
			"root %02d must re-expand to the event's own payload", i)

		hist, herr := s.FileHistory(ctx, degradedEventPath(i))
		require.NoError(t, herr)
		require.Len(t, hist, 1, "exactly one file version per event %02d", i)
		require.Equal(t, rec.Root, hist[0].Root)

		roots[rec.Root] = true
	}
	require.Len(t, roots, degradedEventCount, "20 distinct payloads must produce 20 distinct roots")

	// SessionEnd last, completing §7.3's hook set. Its route ends the session and drains; the
	// store must come out of that with the same 20 tool-use records — no record lost, none
	// invented — and, like every other hook this session, no hookSpecificOutput.
	hookOutputs["SessionEnd"] = p.RunHook(t, "SessionEnd", hookio.Event{
		HookEventName: "SessionEnd", SessionID: degradedSession, CWD: p.Root, TranscriptPath: transcript,
	})
	st, err = s.Stats(ctx)
	require.NoError(t, err)
	require.Equal(t, degradedEventCount, st.ToolUses,
		"the flush drain must leave exactly the 20 recorded tool uses in the store")

	// NO Output.HookSpecificOutput on any hook, and no purely-acting seam ever ran.
	for name, out := range hookOutputs {
		require.Nil(t, out.HookSpecificOutput,
			"hook %s must not emit hookSpecificOutput under degraded-passive", name)
	}
	require.False(t, sessionStartActed.Load(), "SessionStart seam must not run while degraded")
	require.False(t, preCompactActed.Load(), "PreCompact seam must not run while degraded")
	// ObservePrompt is the recording half of §12.1's "L0 and L1 keep running … verbatim capture".
	// It MUST have run; the UserPromptSubmit row of the loop above is what proves the Output it
	// returned ("must-never-be-delivered") went nowhere.
	require.True(t, promptRan.Load(),
		"degraded-passive still records: the ObservePrompt seam must run, only its Output is suppressed")

	// Still degraded at the end: the single clean RunAll must not have restored the session —
	// §12.1 requires two consecutive clean runs.
	require.Equal(t, contract.ModeDegradedPassive.String(), statusMode(t, ctx, p),
		"one clean contract run must not restore ModeFull")
}

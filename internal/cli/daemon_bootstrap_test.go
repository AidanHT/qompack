package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/contract"
	"github.com/qompack/qompack/internal/daemon"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/mcp"
	"github.com/qompack/qompack/internal/obs"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/store"
)

// The daemon bootstrap: installMCPTools and the degrade paths runDaemon walks on the way to
// serving. Every row here is about ORDER or about DEGRADATION, because those are the two things
// that fail silently — a Bind registered after daemon.New still compiles and simply never runs,
// and a store that would not open still leaves a daemon that answers every hook with exit 0.
//
// Nothing here is t.Parallel. contract.DeclareProducer is process-global with no per-test scope
// (internal/contract/producers.go says so outright), and the only isolation the package offers is
// ResetProducers — which a parallel sibling would race. Every row that reaches daemon.New
// therefore brackets itself with ResetProducers so it neither reads another test's declarations
// nor leaves its own behind. No other test in this package reads the producer set, and none of the
// six that do call t.Parallel touch it, so bracketing is sufficient here; it would not be if a
// future parallel test started asserting on producers, and that test would have to serialize
// against these.
//
// On THIS branch SP-11's resident-set block does not exist: installMCPTools builds the promoter
// and calls daemon.InstallMCPOp, and store.Open/dag.Open happen inside daemon.WireObserver. Every
// row below is written against that shape, and says so where the shape is what makes it possible.

// bootstrapProbeTimeout is the dial budget of every readiness wait in this package
// (daemonReachable). It is a hang guard, not a connect budget: each wait already polls inside
// require.Eventually under its own bound, so its verdict is "the listener came up", and a dial that
// has not connected after bootstrapCallDeadline is a hung listener, not a slow one. It was 250 ms
// (and 50 ms at some waits), which a host whose every connect takes longer read as a daemon that
// never came up (audit 2's #80, D61(c);
// TestFixtureReadiness_BootstrapDaemonProbesWithTheHangGuard). config's own ConnectDeadlineMs is no
// basis either: it is tuned for an already-warm daemon.
const bootstrapProbeTimeout = bootstrapCallDeadline

// fixtureProbe is the dial every fixture readiness wait in this package goes through, so a row can
// see the budget a wait dials with. It is ipc.Probe everywhere except in that row.
var fixtureProbe = ipc.Probe

// daemonReachable reports whether the listener at addr accepts a connection within
// bootstrapProbeTimeout. Every require.Eventually that waits for a daemon or a fake server to come
// up in this package calls it instead of ipc.Probe
// (TestFixtureReadinessWaits_GoThroughDaemonReachable).
func daemonReachable(addr ipc.Addr) bool { return fixtureProbe(addr, bootstrapProbeTimeout) }

// bootstrapCallDeadline bounds one admin round trip against a daemon already known to be up. It is
// twenty times the B-F budget, so a failure here means the op did not route, never that retrieval
// was slow.
const bootstrapCallDeadline = 5 * time.Second

// bootstrapUpBound and bootstrapUpTick bound waiting for the daemon's listener to appear.
const (
	bootstrapUpBound = 15 * time.Second
	bootstrapUpTick  = 20 * time.Millisecond
)

// bootstrapLogger records Loud and Warn lines so a degradation row can assert the failure was
// SURFACED and not merely survived (§12: never silent).
//
// It is separate from every other recording logger in the tree because it is the only one this
// package needs: internal/daemon's mcpOpLogger is in another package, and cli's own tests have
// never needed to read a log line before.
type bootstrapLogger struct {
	louds []string
	warns []string
}

// With returns the receiver: these rows assert on messages, never on accumulated fields.
func (l *bootstrapLogger) With(...any) logging.Logger { return l }

// Debug is ignored; no assertion below reads it.
func (l *bootstrapLogger) Debug(string, ...any) {}

// Info is ignored.
func (l *bootstrapLogger) Info(string, ...any) {}

// Warn records the message.
func (l *bootstrapLogger) Warn(msg string, _ ...any) { l.warns = append(l.warns, msg) }

// Error is ignored.
func (l *bootstrapLogger) Error(string, ...any) {}

// Loud records the message, which is what a degradation row asserts on.
func (l *bootstrapLogger) Loud(msg string, _ ...any) { l.louds = append(l.louds, msg) }

// bootstrapLogger must satisfy the seam every composition-root collaborator writes through.
var _ logging.Logger = (*bootstrapLogger)(nil)

// countLouds reports how many recorded Loud messages contain sub.
func (l *bootstrapLogger) countLouds(sub string) int {
	n := 0
	for _, m := range l.louds {
		if strings.Contains(m, sub) {
			n++
		}
	}
	return n
}

// bootstrapProject lays out a project root and returns it, ready for runDaemon.
func bootstrapProject(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	require.NoError(t, paths.EnsureLayout(paths.Of(root)), "EnsureLayout(%s)", root)
	return root
}

// bootstrapDaemon starts runDaemon over root through the real Dispatch surface, waits for its
// listener, and returns a stop function that cancels it and asserts the exit code.
//
// It goes through Dispatch rather than calling runDaemon directly because the exit-code mapping is
// half of what §2.3 promises about this subcommand: a daemon that degraded must still be ExitOK,
// and calling the function directly would assert only that it returned nil.
func bootstrapDaemon(t *testing.T, root string, args ...string) (stop func()) {
	t.Helper()

	contract.ResetProducers()
	t.Cleanup(contract.ResetProducers)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	go func() {
		var out, errw bytes.Buffer
		done <- Dispatch(ctx, []Cmd{{Name: "daemon", Run: runDaemon}},
			append([]string{"qompack", "daemon", "--project", root}, args...),
			Env{Getenv: noEnv, Stdin: bytes.NewReader(nil), Clock: testClock(), HomeDir: t.TempDir()},
			&out, &errw)
	}()

	require.Eventually(t, func() bool {
		addr, err := ipc.Resolve(root)
		return err == nil && daemonReachable(addr)
	}, bootstrapUpBound, bootstrapUpTick, "the daemon at %s never became reachable", root)

	stopped := false
	return func() {
		if stopped {
			return
		}
		stopped = true
		cancel()
		select {
		case code := <-done:
			require.Equal(t, ExitOK, code, "a daemon must never surface a non-zero exit (§2.3)")
		case <-time.After(bootstrapUpBound):
			t.Fatal("runDaemon did not return after its context was cancelled")
		}
	}
}

// bootstrapCall forwards one tools/call to the daemon at root exactly as `qompack mcp` would:
// the same op, the same payload shape, and an EMPTY session, because the stdio process has none to
// send and the daemon is the only party that can resolve one.
func bootstrapCall(t *testing.T, root, name string, args map[string]any) daemon.MCPOpResponse {
	t.Helper()

	addr, err := ipc.Resolve(root)
	require.NoError(t, err, "ipc.Resolve(%s)", root)
	client := ipc.NewClientWithOptions(addr, nopSpool{}, logging.Nop(), obs.New(testClock()),
		ipc.ClientOptions{
			ProjectRoot:     root,
			ConnectDeadline: bootstrapCallDeadline,
			AckDeadline:     bootstrapCallDeadline,
			Clock:           testClock(),
		})
	defer func() { _ = client.Close() }()

	argsRaw, err := json.Marshal(args)
	require.NoError(t, err, "marshalling arguments for %s", name)
	raw, err := json.Marshal(daemon.MCPOpRequest{Kind: daemon.MCPKindCall, Name: name, Args: argsRaw})
	require.NoError(t, err, "marshalling the op payload")

	resp, err := client.Send(context.Background(),
		ipc.Request{Op: ipc.OpMCP, Reply: true, Raw: raw}, bootstrapCallDeadline)
	require.NoError(t, err, "the transport must never error a caller")
	require.True(t, resp.OK, "the mcp op must have routed; err=%q", resp.Err)

	var payload daemon.MCPOpResponse
	require.NoError(t, json.Unmarshal(resp.Data, &payload), "decoding MCPOpResponse from %s", resp.Data)
	return payload
}

// bootstrapBody decodes the single JSON text block a tool result carries into v.
func bootstrapBody(t *testing.T, payload daemon.MCPOpResponse, v any) {
	t.Helper()
	require.False(t, payload.IsError, "a degradation is not a tool error: %v", payload.Content)
	require.Len(t, payload.Content, 1, "a tool result carries exactly one text block")
	require.NoError(t, json.Unmarshal([]byte(payload.Content[0].Text), v),
		"the tool body must be JSON: %s", payload.Content[0].Text)
}

// bootstrapAvailability is the shape every "this build cannot answer" body takes.
type bootstrapAvailability struct {
	Found     bool   `json:"found"`
	Available *bool  `json:"available"`
	Reason    string `json:"reason"`
	State     string `json:"state"`
}

// bootstrapLoudLog returns .qompack/logs/LOUD.log, or "" when nothing was ever Loud.
//
// It is read through paths.ReadFileShared rather than os.ReadFile because the daemon's own
// append-only handle may still be open on Windows, where a non-sharing read of a held file fails
// outright.
func bootstrapLoudLog(t *testing.T, root string) string {
	t.Helper()
	b, err := paths.ReadFileShared(filepath.Join(paths.Of(root).Logs, "LOUD.log"))
	if os.IsNotExist(err) {
		return ""
	}
	require.NoError(t, err, "reading LOUD.log under %s", root)
	return string(b)
}

// bootstrapCountLines reports how many lines of s contain sub.
func bootstrapCountLines(s, sub string) int {
	n := 0
	for _, line := range strings.Split(s, "\n") {
		if strings.Contains(line, sub) {
			n++
		}
	}
	return n
}

// TestBootstrapPromoterFailureDegrades is §8.7's "counting expansions is advisory" made
// executable: a promoter that will not construct costs SP-16 a demand hint and costs this session
// nothing — the op is still installed, and every tool still answers.
//
// The failure is provoked with a threshold of zero rather than with the unwritable state file the
// plan proposed, and the first half of this test is why: mcp.NewPromoter's load() folds EVERY read
// failure into an empty state (§12.3, fail toward doing nothing), so a promotions.json that is a
// DIRECTORY constructs a perfectly good promoter and Louds nothing. That is asserted here rather
// than merely noted, because it is the behaviour a future reader will otherwise re-derive from a
// failing test. A zero threshold is the only construction failure NewPromoter actually has, and it
// is unreachable from runDaemon — config.Load clamps retrieval.promoteAfterExpansions back to its
// default for any value below 1 — so installMCPTools is driven directly.
func TestBootstrapPromoterFailureDegrades(t *testing.T) {
	root := bootstrapProject(t)

	// Half one: an unreadable promotions.json is NOT a construction failure.
	promotions := mcp.PromotionsPath(root)
	require.NoError(t, os.MkdirAll(paths.Long(promotions), 0o755),
		"pre-creating %s as a directory", promotions)

	quiet := &bootstrapLogger{}
	quietOpts := daemon.NewOptions(root, config.Defaults())
	quietOpts.Log = quiet
	installMCPTools(&quietOpts, root, config.Defaults(), quiet, obs.New(testClock()), testClock())
	require.Zero(t, quiet.countLouds("promotion counting disabled"),
		"an unreadable promotions.json is absorbed by load(), not a construction failure: %v", quiet.louds)

	// Half two: the one construction failure NewPromoter has, and the degradation it must produce.
	broken := config.Defaults()
	broken.Retrieval.PromoteAfterExpansions = 0

	log := &bootstrapLogger{}
	opts := daemon.NewOptions(root, broken)
	opts.Log = log
	installMCPTools(&opts, root, broken, log, obs.New(testClock()), testClock())

	require.Equal(t, 1, log.countLouds("promotion counting disabled"),
		"the lost signal must be surfaced exactly once (§12: never silent); louds=%v", log.louds)
	require.Zero(t, log.countLouds("retrieval tools unavailable"),
		"InstallMCPOp must still have succeeded; louds=%v", log.louds)

	h, ok := opts.Handler(ipc.OpMCP)
	require.True(t, ok, "the %q op must still be registered after a promoter failure", ipc.OpMCP)
	require.Len(t, opts.Ops(), 1, "installMCPTools registers exactly the one op")

	// The tools still serve. The context carries no Services, which is precisely the "collaborator
	// absent" shape every handler is written to tolerate, so recall answers rather than panics.
	argsRaw, err := json.Marshal(map[string]any{"query": "pool timeout"})
	require.NoError(t, err)
	raw, err := json.Marshal(daemon.MCPOpRequest{Kind: daemon.MCPKindCall, Name: mcp.ToolRecall, Args: argsRaw})
	require.NoError(t, err)

	resp := h(context.Background(), ipc.Request{Op: ipc.OpMCP, Reply: true, Raw: raw})
	require.True(t, resp.OK, "a tool call must still route after a promoter failure; err=%q", resp.Err)
	require.NotEmpty(t, resp.Data)
}

// TestBootstrapInstallsMCPOpBeforeDaemonNew is the single most important row in this file: it is
// the only thing that proves installMCPTools ran BEFORE daemon.New.
//
// daemon.New applies every Bind and only then calls DeclareProducers, so an InstallMCPOp that ran
// afterwards would bind Services.MCPInitialized too late and CMCPRegistered would never be
// declared. Nothing about that is visible from the outside: the op would still route, every tool
// would still answer, and the mcp.server_registered assertion would report "not-yet-implemented"
// for ever while a working server answered tools/call beside it. The declared producer is the
// only observable that separates the two worlds.
func TestBootstrapInstallsMCPOpBeforeDaemonNew(t *testing.T) {
	root := bootstrapProject(t)

	require.False(t, contract.HasProducer(contract.CMCPRegistered),
		"the producer set must start clean, or this row proves nothing")

	stop := bootstrapDaemon(t, root)
	require.True(t, contract.HasProducer(contract.CMCPRegistered),
		"a daemon that installed the mcp op before New must declare %s", contract.CMCPRegistered)

	// The op is genuinely routable too, so the producer is not being declared by something that
	// merely bound the seam without registering a handler.
	payload := bootstrapCall(t, root, mcp.ToolRecall, map[string]any{"query": "pool timeout"})
	require.False(t, payload.IsError, "recall through the live daemon: %v", payload.Content)

	stop()
}

// TestBootstrapStoreOpenFailureDegrades is §12.3's "everything else fails toward do nothing",
// driven end to end: a store that will not open costs retrieval its content, and costs the session
// nothing else — the daemon still starts, still serves, and still exits 0.
//
// The failure is provoked by putting a regular FILE where store.Open's own ensureStoreDirs wants
// .qompack/tmp/quarantine. That is the one spot that fails store.Open WITHOUT first failing
// runDaemon's own paths.EnsureLayout: EnsureLayout creates tmp/ but never tmp/quarantine, so a
// file there is invisible until store.Open reaches for it, which is exactly the seam this row
// needs. Making .qompack/objects a file instead would fail EnsureLayout, and runDaemon would
// return before WireObserver was ever called.
func TestBootstrapStoreOpenFailureDegrades(t *testing.T) {
	root := bootstrapProject(t)

	quarantine := filepath.Join(paths.Of(root).Tmp, "quarantine")
	require.NoError(t, os.WriteFile(paths.Long(quarantine), []byte("not a directory"), 0o600),
		"pre-creating %s as a regular file is what makes store.Open fail", quarantine)

	stop := bootstrapDaemon(t, root)

	body := bootstrapAvailability{}
	bootstrapBody(t, bootstrapCall(t, root, mcp.ToolRecall, map[string]any{"query": "pool timeout"}), &body)
	require.NotNil(t, body.Available, "an unavailable build must say so explicitly")
	require.False(t, *body.Available, "with no store, recall reports available:false")
	require.Contains(t, body.Reason, "store not present",
		"the reason must name the missing collaborator, not a search that found nothing")

	stop() // releases the log handles, so LOUD.log can be read whole.

	loud := bootstrapLoudLog(t, root)
	require.Equal(t, 1, bootstrapCountLines(loud, "observer unavailable"),
		"the store failure must be Loud exactly once (§12: never silent):\n%s", loud)
	require.Contains(t, loud, "open store",
		"the Loud line must name the step that failed, so this row cannot pass on some OTHER "+
			"observer failure:\n%s", loud)
}

// TestBootstrapClosesStoreExactlyOnce covers the lifetime half of runDaemon's contract: the daemon
// has no services shutdown path, so the ONE deferred Close in runDaemon is what releases the
// store's append-only handles before the process exits.
//
// "Exactly once" is enforced structurally rather than assertably — FSStore.Close is sync.Once
// guarded and runDaemon holds a single defer — so what is checked here is everything that is
// observable from outside: the close happened (every handle released, which on Windows is the
// difference between a removable directory and a locked one), it did not error (runDaemon Warns
// when it does), and the tree it left behind reopens cleanly. A close that never ran fails the
// third assertion on Windows and the fourth everywhere.
func TestBootstrapClosesStoreExactlyOnce(t *testing.T) {
	root := bootstrapProject(t)

	stop := bootstrapDaemon(t, root)
	stop()

	dayLogs, err := filepath.Glob(filepath.Join(paths.Of(root).Logs, "qompack-*.log"))
	require.NoError(t, err)
	require.NotEmpty(t, dayLogs, "a daemon that ran must have written a day log")
	for _, p := range dayLogs {
		b, readErr := paths.ReadFileShared(p)
		require.NoError(t, readErr)
		require.NotContains(t, string(b), "closing the observer's store",
			"runDaemon's deferred Close must not have reported an error in %s", p)
	}

	// Every handle released: on Windows an open append-only handle makes this fail outright, and
	// it is also what a t.TempDir cleanup would have tripped over one assertion later.
	require.NoError(t, os.RemoveAll(paths.Long(paths.Of(root).Index)),
		"the store's index handles must be released by the time runDaemon returns")

	// And the tree the close left behind is usable: a fresh store opens over it without repair.
	reopened, err := store.Open(root, config.Defaults(), store.Deps{Log: logging.Nop(), Clock: testClock()})
	require.NoError(t, err, "the store must reopen over a cleanly closed tree")
	require.NoError(t, reopened.Close())
}

// TestBootstrapDAGOpenFailureDegrades is the store row's sibling one layer down: WireObserver opens
// the DAG after the store, so a DAG that will not load leaves a daemon whose store is live and
// whose L0 capture is off. Retrieval is unaffected, which is the point — `recall` reads the store's
// own index, never the graph.
//
// deps.jsonl is pre-created as a DIRECTORY because that is the shape that fails identically on
// both platforms: os.Open succeeds on a directory everywhere, and the ReadAt that follows fails
// everywhere. Making .qompack/dag itself a file would fail runDaemon's own EnsureLayout first.
func TestBootstrapDAGOpenFailureDegrades(t *testing.T) {
	root := bootstrapProject(t)

	deps := filepath.Join(paths.Of(root).DAG, "deps.jsonl")
	require.NoError(t, os.MkdirAll(paths.Long(deps), 0o755),
		"pre-creating %s as a directory is what makes dag.Open fail", deps)

	stop := bootstrapDaemon(t, root)

	body := bootstrapAvailability{}
	bootstrapBody(t, bootstrapCall(t, root, mcp.ToolRecall, map[string]any{"query": "pool timeout"}), &body)
	require.Nil(t, body.Available,
		"the store opened before the DAG failed, so recall is available and simply finds nothing")
	require.False(t, body.Found, "an empty store finds nothing, which is not an error")

	stop()

	loud := bootstrapLoudLog(t, root)
	require.Equal(t, 1, bootstrapCountLines(loud, "observer unavailable"),
		"a DAG that will not load must be Loud exactly once:\n%s", loud)
	require.Contains(t, loud, "open dag",
		"the Loud line must name the DAG, so this row cannot pass on a store failure instead:\n%s", loud)
}

// TestBootstrapLedgerSkippedWhenStoreIsNil pins what negative knowledge does when the bootstrap
// hands it nothing: `already_tried` reports the documented `unavailable` state with degraded set,
// and never state:"active".
//
// Criterion change (V6 close-out wave 13, retrieval D1): this row used to require the body
// {"available":false,"reason":"elimination ledger not present in this build"} — a shape outside
// already_tried's documented states, which the live lane recorded as a defect (C44-3). The
// invariant the row exists for is unchanged and still asserted: with no ledger the answer is never
// a positive. What changed is only which body says "this cannot be answered": now the tool's own
// `unavailable` state, which no client can mistake for an absence.
//
// The assertion is stronger than its name and weaker than it will be. installMCPTools passes
// opts.Ledger straight through, and at that point nothing has opened a ledger: SP-11's
// WireRehydrator opens one lazily on the first compaction and assigns it to Options only then, so
// ToolDeps.Ledger is nil whether or not the store opened. What is therefore checked here is the
// invariant that survives either way and is the one §12.3 actually cares about: with no ledger, the
// answer is "this build cannot tell you", never a false positive that would refuse a viable
// approach. The ordering claim in the name becomes assertable once the shared-ledger contract
// (SP-19 M0-02) hands the tools the same lazily-opened handle.
func TestBootstrapLedgerSkippedWhenStoreIsNil(t *testing.T) {
	root := bootstrapProject(t)

	quarantine := filepath.Join(paths.Of(root).Tmp, "quarantine")
	require.NoError(t, os.WriteFile(paths.Long(quarantine), []byte("not a directory"), 0o600))

	stop := bootstrapDaemon(t, root)
	defer stop()

	var body struct {
		bootstrapAvailability
		Degraded bool `json:"degraded"`
	}
	bootstrapBody(t, bootstrapCall(t, root, mcp.ToolAlreadyTried, map[string]any{
		"target": "src/pool.ts:connect", "approach": "widen pool timeout",
	}), &body)

	require.Equal(t, "unavailable", body.State, "a daemon with no ledger must answer the unavailable state")
	require.True(t, body.Degraded, "and mark the answer degraded")
	require.Contains(t, body.Reason, "elimination ledger unavailable")
	require.NotEqual(t, "active", body.State,
		"§12.3: with no ledger the answer is never a positive; a wrongly refused approach is the "+
			"failure this subsystem exists to avoid")
}

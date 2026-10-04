package cli

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/contract"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/daemon"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/obs"
)

// Wave 19 statusorder's review (C4.5, D53(a)): on Windows one command-client connect miss inside
// the hot path's 25 ms runtime.daemon.connectDeadlineMs made `qompack status` answer source none and
// say the daemon "did not answer within 10s", for a read that failed in milliseconds. The rows below
// drive that through the real status command and the real ipc client's answer to a missed connect:
// a real client aimed at an address nothing listens on, swapped in through newCommandIPCClient.
//
// No row's verdict rests on a timing margin of its own choosing (D61). fetchDaemonStatus's sends are
// timed on statusSendClock, which these rows replace with a stepClock; daemonListening's dial is
// statusProbeDial, which they replace where a probe is not what the row is about; and a connect that
// succeeds late is a modelled transport (lateConnectClient), not a listener raced against a budget.
// Three rows still use the real transport where it is what they prove, so they still need a real
// connect inside commandConnectDeadline, the product's own budget: the transient-miss row (its
// resend and its second read reach a real daemon), the call-deadline row (a real probe and connect
// to a real server) and TestStatus_RepeatedReadsOfUnchangedStateAgree (status_order_test.go).

// stepClock is a Clock that moves only when a row advances it.
type stepClock struct {
	mu sync.Mutex
	t  time.Time
}

func newStepClock() *stepClock {
	return &stepClock{t: time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)}
}

func (c *stepClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *stepClock) Since(t time.Time) time.Duration { return c.Now().Sub(t) }

func (c *stepClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// useStatusSendClock makes clk time fetchDaemonStatus's sends for the rest of t.
func useStatusSendClock(t *testing.T, clk core.Clock) {
	t.Helper()
	prev := statusSendClock
	statusSendClock = clk
	t.Cleanup(func() { statusSendClock = prev })
}

// useStatusProbe makes probe stand in for daemonListening's dial for the rest of t.
func useStatusProbe(t *testing.T, probe func(ipc.Addr, time.Duration) bool) {
	t.Helper()
	prev := statusProbeDial
	statusProbeDial = probe
	t.Cleanup(func() { statusProbeDial = prev })
}

// probeSeesAListener is a statusProbeDial for a row where a daemon is listening and the probe is not
// what the row is about.
func probeSeesAListener(ipc.Addr, time.Duration) bool { return true }

// connectMissClient sends through miss, a real client whose connect cannot succeed, while misses
// stays positive, and through the real command client after that. misses is shared by every client
// the seam builds, so it counts misses across commands, not per client.
type connectMissClient struct {
	ipc.Client
	miss   ipc.Client
	misses *atomic.Int64
}

func (c connectMissClient) Send(ctx context.Context, req ipc.Request, d time.Duration) (ipc.Response, error) {
	if c.misses.Add(-1) >= 0 {
		return c.miss.Send(ctx, req, d)
	}
	return c.Client.Send(ctx, req, d)
}

func (c connectMissClient) Close() error {
	_ = c.miss.Close()
	return c.Client.Close()
}

// injectCommandConnectMisses makes the next n command-client Sends miss their connect, and returns
// the counter so a row can show the misses really happened. It also records the options every
// command client was built with.
func injectCommandConnectMisses(t *testing.T, n int64) (*atomic.Int64, *[]ipc.ClientOptions) {
	t.Helper()
	nowhere, err := ipc.Resolve(t.TempDir())
	require.NoError(t, err)
	misses := &atomic.Int64{}
	misses.Store(n)
	var built []ipc.ClientOptions
	prev := newCommandIPCClient
	newCommandIPCClient = func(addr ipc.Addr, sp ipc.SpoolWriter, log logging.Logger, m obs.Registry,
		o ipc.ClientOptions,
	) ipc.Client {
		built = append(built, o)
		real := prev(addr, sp, log, m, o)
		o.Self, o.Spawn = "", nil // the missing client never spawns a daemon
		return connectMissClient{Client: real, miss: prev(nowhere, sp, log, m, o), misses: misses}
	}
	t.Cleanup(func() { newCommandIPCClient = prev })
	return misses, &built
}

// statusConnectRead runs `qompack status --json` in root and decodes the part these rows inspect.
func statusConnectRead(t *testing.T, root string) (string, statusOrderEnvelope) {
	t.Helper()
	code, out, errw := fsckDispatchIn(t, root, "status", "--json")
	require.Equal(t, ExitOK, code, "stderr=%s", errw)
	var env statusOrderEnvelope
	require.NoError(t, json.Unmarshal([]byte(out), &env), "stdout=%s", out)
	return out, env
}

// TestCommandClient_HasItsOwnConnectBudget: a command client dials with commandConnectDeadline,
// not the hooks' runtime.daemon.connectDeadlineMs, whatever the project configures for the hooks
// (D60(e)). The hooks' budget itself is left as configured: state.bin still carries it.
//
// Not parallel: it swaps newCommandIPCClient.
func TestCommandClient_HasItsOwnConnectBudget(t *testing.T) {
	root := bootstrapProject(t)
	writeProjectConfig(t, root, `{"runtime":{"daemon":{"connectDeadlineMs":25}}}`)
	_, built := injectCommandConnectMisses(t, 0)

	cfg, _, err := LoadConfigAndReport(config.Env{ProjectRoot: root, HomeDir: t.TempDir(), Getenv: noEnv},
		logging.Nop(), obs.New(testClock()))
	require.NoError(t, err)
	require.Equal(t, 25, cfg.Runtime.Daemon.ConnectDeadlineMs, "the hooks' budget is as configured")

	client := newCommandClient(root, cfg, Env{}, logging.Nop(), obs.New(testClock()), testClock())
	t.Cleanup(func() { _ = client.Close() })
	require.Len(t, *built, 1)
	require.Equal(t, commandConnectDeadline, (*built)[0].ConnectDeadline,
		"a command client must dial with its own budget, not the hot path's connectDeadlineMs")
	require.Less(t, commandConnectDeadline, commandCallDeadline,
		"fetchDaemonStatus tells a connect miss from a call-deadline expiry by the time a send took")
}

// TestStatus_ConnectMissNamesTheConnectBudget: when every connect misses while a daemon listens,
// status names a connect miss within commandConnectDeadline, and does not claim the daemon "did not
// answer within 10s": that deadline never ran. The misses are the real client's own answer to a
// connect that fails; the listener the probe sees and the time the sends take are fixed by the row.
//
// Not parallel: it swaps newCommandIPCClient, statusProbeDial and statusSendClock.
func TestStatus_ConnectMissNamesTheConnectBudget(t *testing.T) {
	root := bootstrapProject(t)
	useStatusProbe(t, probeSeesAListener)
	useStatusSendClock(t, newStepClock())
	misses, _ := injectCommandConnectMisses(t, 1<<30)

	out, env := statusConnectRead(t, root)
	require.NotEqual(t, "daemon", env.Data.Primary.Source, "every connect missed: stdout=%s", out)
	require.Equal(t, int64(1<<30-2), misses.Load(), "both of status's sends must have missed")
	require.NotContains(t, env.Data.Primary.Reason, statusSilentDaemonReason,
		"no call deadline expired, so status must not say the daemon did not answer within it")
	require.Contains(t, env.Data.Primary.Reason, statusConnectMissReason,
		"the reason must name the connect miss")
}

// TestStatus_TransientConnectMissStillReadsTheDaemon: with two sessions registered and a single
// connect miss on the first read, two `status --json` reads of unchanged state agree, both from the
// live daemon (D53(a)). The miss is resent because the send took less than commandCallDeadline on
// the row's own clock, not because a real one happened to return quickly.
//
// Not parallel: bootstrapDaemon resets the process-wide producer set, and the row swaps
// newCommandIPCClient, statusProbeDial and statusSendClock.
func TestStatus_TransientConnectMissStillReadsTheDaemon(t *testing.T) {
	root := bootstrapProject(t)
	stop := bootstrapDaemon(t, root)
	defer stop()
	for _, id := range []core.SessionID{"sess-miss-b", "sess-miss-a"} {
		startStatusOrderSession(t, root, id)
	}
	useStatusProbe(t, probeSeesAListener)
	useStatusSendClock(t, newStepClock())
	misses, _ := injectCommandConnectMisses(t, 1)

	first, env1 := statusConnectRead(t, root)
	require.LessOrEqual(t, misses.Load(), int64(0), "the injected miss must have been reached")
	second, env2 := statusConnectRead(t, root)
	require.Equal(t, "daemon", env1.Data.Primary.Source,
		"read 1 missed its first connect and must still reach the daemon: %s", env1.Data.Primary.Reason)
	require.Equal(t, "daemon", env2.Data.Primary.Source, "read 2: %s", env2.Data.Primary.Reason)
	require.Len(t, env1.Data.Snapshot.Sessions, 2)
	require.Equal(t, first, second, "two status --json reads of unchanged state disagree (D53(a))")
}

// countingClient counts the Sends that reach the client it wraps.
type countingClient struct {
	ipc.Client
	sends *atomic.Int64
}

func (c countingClient) Send(ctx context.Context, req ipc.Request, d time.Duration) (ipc.Response, error) {
	c.sends.Add(1)
	return c.Client.Send(ctx, req, d)
}

// TestStatus_DaemonDisabledIsNamedNotMissed is the wave 19b fix round's finding: with
// runtime.daemon.enabled false and a daemon still listening (the configuration changed while it
// ran), the command client never dials (ipc.Client.Send, step 2) and answers OK false with no text
// at once. Status resent that and reported a connect miss within the 250 ms budget, though no dial
// was ever made. It must name the disabled daemon instead: one send, no probe, no resend, and
// neither the connect-miss, the call-deadline nor the "none is listening" reason. The client is the
// real command client; the probe stands in for the listener that is still running.
//
// Not parallel: it swaps newCommandIPCClient, statusProbeDial and statusSendClock.
func TestStatus_DaemonDisabledIsNamedNotMissed(t *testing.T) {
	root := bootstrapProject(t)
	writeProjectConfig(t, root, `{"runtime":{"daemon":{"enabled":false}}}`)
	requireStatusNamesTheDisabledDaemon(t, root)
}

// TestStatus_StaleStateBinDisabledIsNamed is the wave 19c review's nit. The command client's
// DaemonEnabled is also false when the configuration says true but state.bin says false while the
// daemon that wrote it is alive (daemonEnabledFor, D67(c)): a daemon reloaded runtime.daemon.enabled
// false, rewrote state.bin, and is still running after the key was set back. The reason must not then
// claim only the configuration: it must name state.bin as the other place the key can be false.
// Otherwise the row is TestStatus_DaemonDisabledIsNamedNotMissed's. Once that daemon is gone the
// configuration decides: TestStatus_DeadDaemonsDisabledStateIsNotReportedDisabled.
//
// Not parallel: it swaps newCommandIPCClient, statusProbeDial, statusSendClock and stateDaemonAlive.
func TestStatus_StaleStateBinDisabledIsNamed(t *testing.T) {
	root := bootstrapProject(t)
	useStateDaemonAlive(t, true) // the daemon that wrote state.bin is still running
	st := ipc.StateFromConfig(config.Defaults())
	require.True(t, st.DaemonEnabled, "the project's configuration enables the daemon")
	st.DaemonEnabled = false // what a daemon that reloaded enabled=false wrote before it died
	require.NoError(t, ipc.WriteState(root, st))
	env := requireStatusNamesTheDisabledDaemon(t, root)
	require.Contains(t, env.Data.Primary.Reason, "state.bin",
		"the configuration says true, so the reason must also name the state.bin that says false")
}

// TestStatus_ReasonUsesTheStateTheClientWasBuiltWith is the wave 19c review's second nit (D60(e)).
// The DaemonEnabled fetchDaemonStatus branches on must be the one the command client was built with,
// because that is what decides whether the client dials. When status read state.bin a second time
// for it, a daemon that rewrote state.bin between the two reads (a reload of runtime.daemon.enabled)
// made the reason describe a client that was never built: "disabled" for a client that dialed, or a
// connect miss for one that never dialed. The seam rewrites state.bin from inside client
// construction, after the client's State was read and before status builds its sources, so the row
// does not race anything. The client is the real command client aimed at an address nothing
// listens on, with spawning off; the probe stands in for a listener, and stateDaemonAlive for the
// running daemon that rewrites state.bin, whose false record therefore speaks for the project.
//
// Not parallel: it swaps newCommandIPCClient, statusProbeDial, statusSendClock and stateDaemonAlive.
func TestStatus_ReasonUsesTheStateTheClientWasBuiltWith(t *testing.T) {
	for _, tc := range []struct {
		name       string
		builtWith  bool // state.bin's DaemonEnabled when the client is built
		wantReason string
		wantSends  int64
		wantProbes int64
	}{
		// Built enabled, so the client dials: a missed dial with a listener seen is resent and named.
		{
			name: "enabled then disabled", builtWith: true,
			wantReason: statusConnectMissReason, wantSends: 2, wantProbes: 1,
		},
		// Built disabled, so the client never dials: asked once, never probed, named disabled.
		{
			name: "disabled then enabled", builtWith: false,
			wantReason: statusDaemonDisabledReason, wantSends: 1, wantProbes: 0,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := bootstrapProject(t)
			useStateDaemonAlive(t, true)
			st := ipc.StateFromConfig(config.Defaults())
			require.True(t, st.DaemonEnabled, "the project's configuration enables the daemon")
			st.DaemonEnabled = tc.builtWith
			require.NoError(t, ipc.WriteState(root, st))

			var probes atomic.Int64
			useStatusProbe(t, func(ipc.Addr, time.Duration) bool {
				probes.Add(1)
				return true
			})
			useStatusSendClock(t, newStepClock())
			nowhere, err := ipc.Resolve(t.TempDir())
			require.NoError(t, err)
			var sends atomic.Int64
			var built []ipc.ClientOptions
			prev := newCommandIPCClient
			newCommandIPCClient = func(_ ipc.Addr, sp ipc.SpoolWriter, log logging.Logger, m obs.Registry,
				o ipc.ClientOptions,
			) ipc.Client {
				built = append(built, o)
				changed := st
				changed.DaemonEnabled = !tc.builtWith // the daemon rewrote state.bin after this read
				require.NoError(t, ipc.WriteState(root, changed))
				o.Self, o.Spawn = "", nil // the stand-in never spawns a daemon
				return countingClient{Client: prev(nowhere, sp, log, m, o), sends: &sends}
			}
			t.Cleanup(func() { newCommandIPCClient = prev })

			out, env := statusConnectRead(t, root)
			require.Len(t, built, 1, "one command client per status run")
			require.Equal(t, tc.builtWith, built[0].State.DaemonEnabled,
				"the client is built with the state.bin read before the rewrite")
			require.Equal(t, !tc.builtWith, ipc.ReadState(root, config.Defaults()).DaemonEnabled,
				"the seam rewrote state.bin before status built its sources")
			require.NotEqual(t, "daemon", env.Data.Primary.Source, "stdout=%s", out)
			require.Contains(t, env.Data.Primary.Reason, tc.wantReason,
				"the reason must describe the client that was built, not the rewritten state.bin")
			require.Equal(t, tc.wantSends, sends.Load())
			require.Equal(t, tc.wantProbes, probes.Load())
		})
	}
}

// requireStatusNamesTheDisabledDaemon runs status --json over root, whose command client must be
// built with the daemon disabled, against a probe that reports a listener, and requires the
// disabled-daemon answer: one send, no probe, and statusDaemonDisabledReason alone.
func requireStatusNamesTheDisabledDaemon(t *testing.T, root string) statusOrderEnvelope {
	t.Helper()
	var probes atomic.Int64
	useStatusProbe(t, func(ipc.Addr, time.Duration) bool {
		probes.Add(1)
		return true // a daemon started before the configuration changed is still listening
	})
	useStatusSendClock(t, newStepClock())
	var sends atomic.Int64
	var built []ipc.ClientOptions
	prev := newCommandIPCClient
	newCommandIPCClient = func(addr ipc.Addr, sp ipc.SpoolWriter, log logging.Logger, m obs.Registry,
		o ipc.ClientOptions,
	) ipc.Client {
		built = append(built, o)
		return countingClient{Client: prev(addr, sp, log, m, o), sends: &sends}
	}
	t.Cleanup(func() { newCommandIPCClient = prev })

	out, env := statusConnectRead(t, root)
	require.Len(t, built, 1, "one command client per status run")
	require.False(t, built[0].State.DaemonEnabled, "the command client is built with the daemon disabled")
	require.NotEqual(t, "daemon", env.Data.Primary.Source, "stdout=%s", out)
	require.Equal(t, int64(1), sends.Load(), "a client that never dials is asked once, not resent")
	require.Zero(t, probes.Load(), "with the daemon disabled, whether one listens decides nothing")
	require.Contains(t, env.Data.Primary.Reason, statusDaemonDisabledReason,
		"the reason must say the daemon is disabled for this project")
	require.NotContains(t, env.Data.Primary.Reason, statusConnectMissReason, "no dial was made")
	require.NotContains(t, env.Data.Primary.Reason, statusSilentDaemonReason, "no call deadline ran")
	require.NotContains(t, env.Data.Primary.Reason, statusNoDaemonReason)
	return env
}

// lateConnectClient is the transport to a listener that accepts a connection only readyAt after a
// dial starts: busy until then, as a go-winio listener is while it re-arms. A dial whose budget
// reaches readyAt connects and is answered with answer; one whose budget falls short gives up when
// the budget runs out and gets the real client's answer to a missed connect (OK false, no text).
// Time passes on clk alone, by as much as the real dial would have waited, so no verdict depends on
// how fast this machine runs.
type lateConnectClient struct {
	budget  time.Duration
	readyAt time.Duration
	clk     *stepClock
	sends   *atomic.Int64
	answer  ipc.Response
}

func (c lateConnectClient) Send(_ context.Context, req ipc.Request, _ time.Duration) (ipc.Response, error) {
	if req.Op != ipc.OpStatus {
		return ipc.Response{OK: false, Err: "lateConnectClient answers only " + string(ipc.OpStatus)}, nil
	}
	c.sends.Add(1)
	if c.budget < c.readyAt {
		c.clk.Advance(c.budget)
		return ipc.Response{OK: false}, nil
	}
	c.clk.Advance(c.readyAt)
	return c.answer, nil
}

func (lateConnectClient) Close() error { return nil }

// TestCommandClient_LateConnectWithinItsBudgetReadsTheDaemon is the wave 19b review's nit: a
// connect that succeeds only after the hot path's runtime.daemon.connectDeadlineMs, but within
// commandConnectDeadline, reads the daemon for a command client. The same listener reached with the
// hot path's budget, as command clients dialed before D60(e), and a listener that stays busy past
// commandConnectDeadline itself, both read as a connect miss that names the 250 ms budget: sent twice,
// never reported as "did not answer within 10s" and never as "none is listening".
//
// Not parallel: it swaps newCommandIPCClient, statusProbeDial and statusSendClock.
func TestCommandClient_LateConnectWithinItsBudgetReadsTheDaemon(t *testing.T) {
	root := bootstrapProject(t)
	hotPath := time.Duration(config.Defaults().Runtime.Daemon.ConnectDeadlineMs) * time.Millisecond
	// Halfway between the two budgets: too late for the hot path's, in time for a command's.
	inBudget := hotPath + (commandConnectDeadline-hotPath)/2
	require.Less(t, hotPath, inBudget)
	require.Less(t, inBudget, commandConnectDeadline)

	snap, err := json.Marshal(daemon.StatusSnapshot{Mode: contract.ModeFull.String(), Hot: "sync"})
	require.NoError(t, err)

	var probed []time.Duration
	useStatusProbe(t, func(_ ipc.Addr, d time.Duration) bool {
		probed = append(probed, d)
		return true // the listener was free when the probe dialed; the request's dial is what is late
	})

	for _, tc := range []struct {
		name     string
		readyAt  time.Duration
		hotPath  bool // dial with the hooks' budget, as command clients did before D60(e)
		wantLive bool
	}{
		{name: "command budget, accepted after the hot path's", readyAt: inBudget, wantLive: true},
		{name: "hot path budget, the same listener", readyAt: inBudget, hotPath: true},
		{name: "command budget, busy past it", readyAt: commandConnectDeadline + hotPath},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clk := newStepClock()
			useStatusSendClock(t, clk)
			probed = probed[:0]
			var sends atomic.Int64
			var budgets []time.Duration
			prev := newCommandIPCClient
			newCommandIPCClient = func(_ ipc.Addr, _ ipc.SpoolWriter, _ logging.Logger, _ obs.Registry,
				o ipc.ClientOptions,
			) ipc.Client {
				budget := o.ConnectDeadline
				if tc.hotPath {
					budget = time.Duration(o.State.ConnectDeadlineMs) * time.Millisecond
				}
				budgets = append(budgets, budget)
				return lateConnectClient{
					budget: budget, readyAt: tc.readyAt, clk: clk, sends: &sends,
					answer: ipc.Response{OK: true, Data: snap},
				}
			}
			t.Cleanup(func() { newCommandIPCClient = prev })
			start := clk.Now()

			out, env := statusConnectRead(t, root)
			require.Len(t, budgets, 1, "one command client per status run")
			require.Equal(t, []time.Duration{statusProbeTimeout}, probed, "one probe, at its own budget")
			if tc.hotPath {
				require.Equal(t, hotPath, budgets[0], "the hot path's budget as state.bin carries it")
			} else {
				require.Equal(t, commandConnectDeadline, budgets[0])
			}

			if tc.wantLive {
				require.Equal(t, "daemon", env.Data.Primary.Source,
					"a connect accepted within the command budget must read the daemon: %s", out)
				require.Equal(t, int64(1), sends.Load(), "an answered read is not resent")
				require.Equal(t, tc.readyAt, clk.Since(start))
				return
			}
			require.NotEqual(t, "daemon", env.Data.Primary.Source, "stdout=%s", out)
			require.Equal(t, int64(2), sends.Load(), "a connect miss is resent once")
			require.Equal(t, 2*budgets[0], clk.Since(start), "each send waited out its dial budget")
			require.Contains(t, env.Data.Primary.Reason, statusConnectMissReason,
				"a late connect must read as a connect miss naming the command budget")
			require.NotContains(t, env.Data.Primary.Reason, statusSilentDaemonReason)
			require.NotContains(t, env.Data.Primary.Reason, statusNoDaemonReason)
		})
	}
}

// TestStatus_CallDeadlineExpiryStillSaysSilent: a daemon that accepts the status request and never
// replies still reads as statusSilentDaemonReason, because there commandCallDeadline did expire,
// and the request is sent once: an expired call deadline is not retried. It waits out the real
// commandCallDeadline once. The verdict needs no margin: the client arms its read deadline after
// the send starts, so on the monotonic clock the send cannot take less than commandCallDeadline.
func TestStatus_CallDeadlineExpiryStillSaysSilent(t *testing.T) {
	root := mcpCmdRoot(t)
	addr, err := ipc.Resolve(root)
	require.NoError(t, err)
	srv, err := ipc.NewServer(addr, logging.Nop(), obs.New(testClock()), 0)
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	release := make(chan struct{})
	var calls atomic.Int64
	served := make(chan struct{})
	go func() {
		defer close(served)
		_ = srv.Serve(ctx, func(hctx context.Context, _ ipc.Request) ipc.Response {
			calls.Add(1)
			select { // never answers within the call deadline
			case <-release:
			case <-hctx.Done():
			}
			return ipc.Response{OK: false, Err: "released"}
		})
	}()
	t.Cleanup(func() {
		close(release)
		cancel()
		_ = srv.Close()
		<-served
	})

	cfg := config.Defaults()
	client := newCommandClient(root, cfg, Env{}, logging.Nop(), obs.New(testClock()), testClock())
	t.Cleanup(func() { _ = client.Close() })

	_, _, err = fetchDaemonStatus(context.Background(), client, daemonListening(root))
	require.Error(t, err)
	require.Equal(t, statusSilentDaemonReason, err.Error())
	require.Equal(t, int64(1), calls.Load(), "an expired call deadline must not be retried")
}

// TestStatusProbe_HasTheCommandConnectBudget: daemonListening's dial gets the command client's own
// connect budget. A smaller probe budget makes the probe the weakest dial on the status path, and a
// probe miss on a live daemon is reported as "none is listening" and never resent.
func TestStatusProbe_HasTheCommandConnectBudget(t *testing.T) {
	require.Equal(t, commandConnectDeadline, statusProbeTimeout,
		"the liveness probe must dial with the same budget as the command client")
}

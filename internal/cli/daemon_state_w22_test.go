package cli

import (
	"bytes"
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/contract"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/daemon"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/obs"
	"github.com/qompack/qompack/internal/paths"
)

// Audit 2's findings #15 and #67 (wave 22, D67(c)): a state.bin that says DaemonEnabled=false is
// trusted only while its daemon is alive; otherwise the configuration decides.

// staleDisabledState writes the state.bin a daemon leaves when it reloaded runtime.daemon.enabled
// false and then stopped without a clean stop: every other field as the defaults give it.
func staleDisabledState(t *testing.T, root string) {
	t.Helper()
	st := ipc.StateFromConfig(config.Defaults())
	require.True(t, st.DaemonEnabled, "the configuration enables the daemon")
	st.DaemonEnabled = false
	require.NoError(t, ipc.WriteState(root, st))
}

// listenAsTheDaemon stands up a listener at root's daemon address that answers every request OK, for
// the rest of t: a daemon that is alive.
func listenAsTheDaemon(t *testing.T, root string) {
	t.Helper()
	addr, err := ipc.Resolve(root)
	require.NoError(t, err)
	srv, err := ipc.NewServer(addr, logging.Nop(), obs.New(testClock()), 0)
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	served := make(chan struct{})
	go func() {
		defer close(served)
		_ = srv.Serve(ctx, func(context.Context, ipc.Request) ipc.Response { return ipc.Response{OK: true} })
	}()
	t.Cleanup(func() {
		cancel()
		_ = srv.Close()
		<-served
	})
}

// holdTheDaemonLock takes root's daemon lock for this process for the rest of t: a daemon that holds
// the lock, whether or not it is listening yet.
func holdTheDaemonLock(t *testing.T, root string) {
	t.Helper()
	addr, err := ipc.Resolve(root)
	require.NoError(t, err)
	l, err := daemon.AcquireLock(root, addr, core.SystemClock())
	require.NoError(t, err)
	t.Cleanup(func() { _ = l.Release() })
}

// TestDaemonClientState_DeadDaemonsDisabledStateDefersToTheConfiguration is #15/#67 under D67(c): a
// state.bin that says DaemonEnabled=false is trusted only while its daemon is alive. Its daemon is
// gone here (no lock, no listener) and the configuration says true, so the configuration decides
// and the client may dial and spawn again. Before, nothing ever rewrote that state.bin.
func TestDaemonClientState_DeadDaemonsDisabledStateDefersToTheConfiguration(t *testing.T) {
	root := bootstrapProject(t)
	staleDisabledState(t, root)
	require.True(t, daemonClientState(root, config.Defaults()).DaemonEnabled,
		"no daemon holds the lock or answers, so the configuration decides")

	cfg := config.Defaults()
	cfg.Runtime.Daemon.Enabled = false
	require.False(t, daemonClientState(root, cfg).DaemonEnabled,
		"a configuration that says false is never overridden")
}

// TestDaemonClientState_LiveDaemonsDisabledStateIsTrusted is the other direction: while the daemon
// that wrote DaemonEnabled=false is alive — it answers, or it holds the lock — that state.bin decides.
func TestDaemonClientState_LiveDaemonsDisabledStateIsTrusted(t *testing.T) {
	for name, alive := range map[string]func(*testing.T, string){
		"answers":        listenAsTheDaemon,
		"holds the lock": holdTheDaemonLock,
	} {
		t.Run(name, func(t *testing.T) {
			root := bootstrapProject(t)
			staleDisabledState(t, root)
			alive(t, root)
			require.False(t, daemonClientState(root, config.Defaults()).DaemonEnabled,
				"the live daemon's own state.bin says it is disabled")
		})
	}
}

// TestSessionStart_DeadDaemonsDisabledStateStillStartsADaemon is #67's hook half: session-start
// is the designated daemon starter, and it skipped the start whenever state.bin said
// DaemonEnabled=false, so a project whose daemon died in that state never got one back. The
// pre-send step (ensureDaemonRunning in production) must now see the daemon enabled; while a daemon
// holds the lock, it still sees state.bin's false.
func TestSessionStart_DeadDaemonsDisabledStateStillStartsADaemon(t *testing.T) {
	for _, alive := range []bool{false, true} {
		t.Run(map[bool]string{false: "daemon gone", true: "daemon holds the lock"}[alive], func(t *testing.T) {
			root := bootstrapProject(t)
			staleDisabledState(t, root)
			if alive {
				holdTheDaemonLock(t, root)
			}
			var mu sync.Mutex
			var seen []bool
			spec := sessionStartSpec(func(_, _ string, st ipc.State, _ core.Clock, _ hookBudget) {
				mu.Lock()
				defer mu.Unlock()
				seen = append(seen, st.DaemonEnabled)
			})
			payload := strings.Replace(string(entryPayload(t, "SessionStart", root)), `"source":"compact"`,
				`"source":"startup"`, 1)
			var out, errw bytes.Buffer
			require.NoError(t, doHook(spec)(context.Background(), Env{
				Getenv:  envWith(map[string]string{"QOMPACK_PROJECT_ROOT": root}),
				Stdin:   strings.NewReader(payload),
				Clock:   testClock(),
				HomeDir: t.TempDir(),
			}, nil, &out, &errw), "stderr=%s", errw.String())
			mu.Lock()
			defer mu.Unlock()
			require.Equal(t, []bool{!alive}, seen,
				"session-start's daemon start sees the configuration once state.bin's daemon is gone")
		})
	}
}

// TestStatus_DeadDaemonsDisabledStateIsNotReportedDisabled is #15's status half: with the daemon
// that wrote DaemonEnabled=false gone, status asks for a daemon like any enabled project and says
// none is listening, rather than naming a disabled daemon nothing has disabled.
func TestStatus_DeadDaemonsDisabledStateIsNotReportedDisabled(t *testing.T) {
	root := bootstrapProject(t)
	staleDisabledState(t, root)
	out, env := statusConnectRead(t, root)
	require.NotEqual(t, "daemon", env.Data.Primary.Source, "stdout=%s", out)
	require.NotContains(t, env.Data.Primary.Reason, statusDaemonDisabledReason, "stdout=%s", out)
	require.Contains(t, env.Data.Primary.Reason, "none is listening", "stdout=%s", out)
}

// useStateDaemonAlive makes stateDaemonAlive answer alive for every root for the rest of t: the
// daemon that wrote the row's state.bin is still running. Rows that call it are not parallel.
func useStateDaemonAlive(t *testing.T, alive bool) {
	t.Helper()
	prev := stateDaemonAlive
	stateDaemonAlive = func(string) bool { return alive }
	t.Cleanup(func() { stateDaemonAlive = prev })
}

// The mode sibling of #15/#67 in D67(c)'s class (wave 22 fix round 2): a state.bin that says
// runtime.mode off is trusted only while its daemon is alive; otherwise the configuration decides.
// A daemon writes Mode=off when a session start reaches it while the configuration says off, and only
// a clean stop removes the file, so one that died left every hook returning before it recorded or
// spooled anything, session-start from starting the daemon that would rewrite the file, and status
// and the MCP server reporting the project off, for good.

// staleModeOffState writes the state.bin a daemon leaves when it ran under runtime.mode off and then
// stopped without a clean stop: every other field as the defaults give it.
func staleModeOffState(t *testing.T, root string) {
	t.Helper()
	st := ipc.StateFromConfig(config.Defaults())
	require.NotEqual(t, contract.ModeOff, st.Mode, "the configuration does not say off")
	st.Mode = contract.ModeOff
	require.NoError(t, ipc.WriteState(root, st))
}

// TestDaemonClientState_DeadDaemonsModeOffStateDefersToTheConfiguration: with the daemon that wrote
// Mode=off gone (no lock, no listener), the command client and the MCP server's client work under the
// configuration's mode. A configuration that says off is never overridden, whatever state.bin says.
func TestDaemonClientState_DeadDaemonsModeOffStateDefersToTheConfiguration(t *testing.T) {
	root := bootstrapProject(t)
	staleModeOffState(t, root)
	require.Equal(t, contract.ModeFull, daemonClientState(root, config.Defaults()).Mode,
		"no daemon holds the lock or answers, so the configuration decides")

	cfg := config.Defaults()
	cfg.Runtime.Mode = configModeOff
	require.Equal(t, contract.ModeOff, daemonClientState(root, cfg).Mode)
	require.NoError(t, ipc.WriteState(root, ipc.StateFromConfig(config.Defaults())))
	require.Equal(t, contract.ModeOff, daemonClientState(root, cfg).Mode,
		"a configuration that says off decides over a state.bin that does not")
}

// TestDaemonClientState_LiveDaemonsModeOffStateIsTrusted is the other direction: while the daemon
// that wrote Mode=off is alive — it answers, or it holds the lock — that state.bin decides.
func TestDaemonClientState_LiveDaemonsModeOffStateIsTrusted(t *testing.T) {
	for name, alive := range map[string]func(*testing.T, string){
		"answers":        listenAsTheDaemon,
		"holds the lock": holdTheDaemonLock,
	} {
		t.Run(name, func(t *testing.T) {
			root := bootstrapProject(t)
			staleModeOffState(t, root)
			alive(t, root)
			require.Equal(t, contract.ModeOff, daemonClientState(root, config.Defaults()).Mode,
				"the live daemon's own state.bin says runtime.mode is off")
		})
	}
}

// TestSessionStart_DeadDaemonsModeOffStateStillStartsADaemon is the hook half: with the daemon gone,
// session-start reaches its daemon start (the pre-send step) under the configuration's mode, and the
// start it could not deliver is spooled, not dropped. While a daemon holds the lock, or the
// configuration itself says off, the hook still answers at once and writes nothing.
func TestSessionStart_DeadDaemonsModeOffStateStillStartsADaemon(t *testing.T) {
	cases := []struct {
		name          string
		alive, cfgOff bool
	}{
		{"daemon gone", false, false},
		{"daemon holds the lock", true, false},
		{"daemon gone, configuration off", false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := bootstrapProject(t)
			if tc.cfgOff {
				writeProjectConfig(t, root, `{"runtime":{"mode":"off"}}`)
			}
			staleModeOffState(t, root)
			if tc.alive {
				holdTheDaemonLock(t, root)
			}
			var mu sync.Mutex
			var seen []contract.Mode
			spec := sessionStartSpec(func(_, _ string, st ipc.State, _ core.Clock, _ hookBudget) {
				mu.Lock()
				defer mu.Unlock()
				seen = append(seen, st.Mode)
			})
			payload := strings.Replace(string(entryPayload(t, "SessionStart", root)), `"source":"compact"`,
				`"source":"startup"`, 1)
			var out, errw bytes.Buffer
			require.NoError(t, doHook(spec)(context.Background(), Env{
				Getenv:  envWith(map[string]string{"QOMPACK_PROJECT_ROOT": root}),
				Stdin:   strings.NewReader(payload),
				Clock:   testClock(),
				HomeDir: t.TempDir(),
			}, nil, &out, &errw), "stderr=%s", errw.String())
			spooled, err := ipc.SpoolFiles(paths.Of(root).Spool)
			require.NoError(t, err)
			mu.Lock()
			defer mu.Unlock()
			if tc.alive || tc.cfgOff {
				require.Empty(t, seen, "runtime.mode is off: no daemon start")
				require.Empty(t, spooled, "runtime.mode is off: nothing recorded")
				require.Equal(t, "{}\n", out.String())
				return
			}
			require.Equal(t, []contract.Mode{contract.ModeFull}, seen,
				"session-start's daemon start sees the configuration once state.bin's daemon is gone")
			require.Len(t, spooled, 1, "the start no daemon answered is spooled for the daemon it starts")
		})
	}
}

// TestStatus_DeadDaemonsModeOffStateIsNotReportedOff is the status half: with the daemon that wrote
// Mode=off gone, status asks for a daemon like any project whose configuration is not off and says
// none is listening, rather than naming a mode nothing has set.
func TestStatus_DeadDaemonsModeOffStateIsNotReportedOff(t *testing.T) {
	root := bootstrapProject(t)
	staleModeOffState(t, root)
	out, env := statusConnectRead(t, root)
	require.NotEqual(t, "daemon", env.Data.Primary.Source, "stdout=%s", out)
	require.NotContains(t, env.Data.Primary.Reason, statusModeOffReason, "stdout=%s", out)
	require.Contains(t, env.Data.Primary.Reason, "none is listening", "stdout=%s", out)
}

// TestStatus_LiveDaemonsModeOffStateIsReportedOff: while the daemon that wrote Mode=off is alive, its
// state.bin speaks for the project and status names the mode.
func TestStatus_LiveDaemonsModeOffStateIsReportedOff(t *testing.T) {
	root := bootstrapProject(t)
	staleModeOffState(t, root)
	useStateDaemonAlive(t, true)
	out, env := statusConnectRead(t, root)
	require.Contains(t, env.Data.Primary.Reason, statusModeOffReason, "stdout=%s", out)
}

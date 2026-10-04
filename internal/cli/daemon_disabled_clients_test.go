package cli

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/obs"
	"github.com/qompack/qompack/internal/paths"
)

// disabledClientSendDeadline bounds the one Send each case below makes. Nothing listens at the
// project's address, so the dial fails at once; the value only has to exceed that.
const disabledClientSendDeadline = 500 * time.Millisecond

// TestDaemonClients_HonourDisabledDaemonOverStaleState pins runtime.daemon.enabled: false for the two
// user commands whose client can lazily spawn a daemon: `qompack mcp` (newMCPClient) and the
// command frontends such as `qompack status` (newCommandClient).
//
// Both read state.bin, which a daemon writes with the DaemonEnabled it was started with and which a
// daemon that died without a clean stop leaves behind. Before this fix neither client consulted the
// configuration once a state record existed, so a stale record written while the daemon was enabled
// made them spawn a daemon for a project the operator has since disabled — the hook path already
// ANDs the two (doHook, FR-6). The spawner is replaced by a recorder, so no process is ever started;
// the enabled case proves the recorder is reached when a spawn is allowed.
func TestDaemonClients_HonourDisabledDaemonOverStaleState(t *testing.T) {
	var mu sync.Mutex
	var spawned []string
	prev := spawnDaemon
	spawnDaemon = func(root, _ string) error {
		mu.Lock()
		defer mu.Unlock()
		spawned = append(spawned, root)
		return nil
	}
	t.Cleanup(func() { spawnDaemon = prev })

	ctors := map[string]func(string, config.Config, Env, logging.Logger, obs.Registry, core.Clock) ipc.Client{
		"mcp": newMCPClient,
		"commands": func(root string, cfg config.Config, env Env, log logging.Logger, reg obs.Registry,
			clk core.Clock,
		) ipc.Client {
			return newCommandClient(root, cfg, env, log, reg, clk)
		},
	}
	for name, ctor := range ctors {
		for _, enabled := range []bool{false, true} {
			t.Run(name+map[bool]string{false: "/disabled", true: "/enabled"}[enabled], func(t *testing.T) {
				mu.Lock()
				spawned = nil
				mu.Unlock()

				root := t.TempDir()
				require.NoError(t, paths.EnsureLayout(paths.Of(root)))
				// The stale record: written from the defaults, so it says the daemon is enabled.
				require.NoError(t, ipc.WriteState(root, ipc.StateFromConfig(config.Defaults())))
				cfg := config.Defaults()
				cfg.Runtime.Daemon.Enabled = enabled

				clk := testClock()
				env := Env{Self: filepath.Join(t.TempDir(), "does-not-exist")}
				c := ctor(root, cfg, env, logging.Nop(), obs.New(clk), clk)
				t.Cleanup(func() { _ = c.Close() })
				resp, _ := c.Send(context.Background(), ipc.Request{Op: ipc.OpStatus, Reply: true},
					disabledClientSendDeadline)
				require.False(t, resp.OK, "nothing is listening, so no request can succeed")

				mu.Lock()
				got := append([]string(nil), spawned...)
				mu.Unlock()
				_, serr := os.Stat(filepath.Join(paths.Of(root).Run, "spawn.lock"))
				if !enabled {
					require.Empty(t, got, "runtime.daemon.enabled=false must never spawn a daemon, "+
						"whatever a stale state.bin says")
					require.True(t, os.IsNotExist(serr),
						"runtime.daemon.enabled=false must never claim run/spawn.lock (stat err=%v)", serr)
					return
				}
				require.Equal(t, []string{root}, got, "an enabled daemon is still spawned lazily")
			})
		}
	}
}

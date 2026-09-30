package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/daemon"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/mcp"
	"github.com/qompack/qompack/internal/obs"
	"github.com/qompack/qompack/internal/paths"
)

// A config reload reaches the retrieval tools of a daemon runDaemon composed (V6 close-out D49).
// The candidate 4 live re-run's UAT-09 changed an eliminations key mid-session; the daemon logged
// "config reloaded changed=[eliminations.staleResponse]" and already_tried kept the old form,
// because installMCPTools handed the tools the configuration the daemon started with. This row
// drives the real composition root: record_eliminated without a scope takes
// eliminations.defaultScope, and with no live session a session-scoped record is refused, so the
// same call answers differently the moment a reloaded "project" reaches the tool.

// writeReloadConfig writes body as root's .qompack/config.json.
func writeReloadConfig(t *testing.T, root, body string) {
	t.Helper()
	p := filepath.Join(paths.Of(root).Dot, "config.json")
	require.NoError(t, os.WriteFile(paths.Long(p), []byte(body), 0o600))
}

// adminReload forces the daemon at root to reload its configuration and returns the keys it
// reported, applied and held for a restart.
func adminReload(t *testing.T, root string) (changed, restart []string) {
	t.Helper()
	body := adminReloadBody(t, root)
	return body.Changed, body.Restart
}

// adminReloadReply is admin.reload's answer.
type adminReloadReply struct {
	Changed  []string `json:"changed"`
	Restart  []string `json:"restart_required"`
	NoEffect []string `json:"no_effect"`
}

// adminReloadBody forces the daemon at root to reload its configuration and returns its whole answer.
func adminReloadBody(t *testing.T, root string) adminReloadReply {
	t.Helper()
	addr, err := ipc.Resolve(root)
	require.NoError(t, err)
	client := ipc.NewClientWithOptions(addr, nopSpool{}, logging.Nop(), obs.New(testClock()),
		ipc.ClientOptions{
			ProjectRoot: root, ConnectDeadline: bootstrapCallDeadline, AckDeadline: bootstrapCallDeadline,
			Clock: testClock(),
		})
	defer func() { _ = client.Close() }()
	resp, err := client.Send(context.Background(), ipc.Request{Op: ipc.OpAdminReload, Reply: true}, bootstrapCallDeadline)
	require.NoError(t, err)
	require.True(t, resp.OK, "admin.reload: %s", resp.Err)
	var body adminReloadReply
	require.NoError(t, json.Unmarshal(resp.Data, &body), "%s", resp.Data)
	return body
}

func TestDaemonConfigReload_ReachesTheRetrievalTools(t *testing.T) {
	root := bootstrapProject(t)
	writeReloadConfig(t, root, `{"eliminations":{"defaultScope":"session","requireEvidence":false}}`)
	stop := bootstrapDaemon(t, root)
	defer stop()

	args := map[string]any{
		"target": "export delimiter", "approach": "semicolon", "reason": "the user corrected it",
	}
	first := bootstrapCall(t, root, mcp.ToolRecordEliminated, args)
	require.True(t, first.IsError, "session scope with no live session is refused: %v", first.Content)

	writeReloadConfig(t, root, `{"eliminations":{"defaultScope":"project","requireEvidence":false}}`)
	changed, restart := adminReload(t, root)
	require.Contains(t, changed, "eliminations.defaultScope")
	require.Empty(t, restart)

	var body struct {
		Scope string `json:"scope"`
	}
	bootstrapBody(t, bootstrapCall(t, root, mcp.ToolRecordEliminated, args), &body)
	require.Equal(t, "project", body.Scope, "the reloaded default scope reaches the next tool call")
}

// TestDaemonConfigReload_SaysWhichKeysNeedARestart: a key the running daemon cannot apply is not
// reported as applied. The reload names it, and the daemon keeps running on the value it started
// with.
func TestDaemonConfigReload_SaysWhichKeysNeedARestart(t *testing.T) {
	root := bootstrapProject(t)
	stop := bootstrapDaemon(t, root)
	defer stop()

	writeReloadConfig(t, root, `{"sketches":{"bloom":{"capacity":5000}},"runtime":{"rehydrate":{"minTokens":150,"maxTokens":150}}}`)
	changed, restart := adminReload(t, root)
	require.Equal(t, []string{"sketches.bloom.capacity"}, restart)
	require.NotContains(t, changed, "sketches.bloom.capacity")
	require.Contains(t, changed, "runtime.rehydrate.maxTokens")
	require.Equal(t, 1, bootstrapCountLines(bootstrapLoudLog(t, root), "needs a daemon restart"))
}

// TestDaemonConfigReload_NamesChunkAndInertKeys: through the real composition root, admin.reload
// names a store.chunk.* change as needing a restart (nothing re-reads the chunker's boundaries after
// store.Open) and a key nothing in this build reads as having no effect, and reports neither as
// changed.
func TestDaemonConfigReload_NamesChunkAndInertKeys(t *testing.T) {
	root := bootstrapProject(t)
	stop := bootstrapDaemon(t, root)
	defer stop()

	writeReloadConfig(t, root, `{"store":{"chunk":{"min":1024,"target":9999,"max":16384}},`+
		`"runtime":{"logging":{"level":"debug"},"daemon":{"maxSessions":3}}}`)
	body := adminReloadBody(t, root)
	require.Equal(t, []string{"runtime.daemon.maxSessions"}, body.Changed)
	require.Equal(t, []string{"store.chunk.target"}, body.Restart)
	require.Equal(t, []string{"runtime.logging.level"}, body.NoEffect)
}

// TestWireScheduler_ReadsTheLiveConfiguration: the composition root hands the scheduler runtime the
// daemon's live configuration rather than the one it started with, which is what makes
// daemon.TestConfigReload_ReachesTheSchedulerRuntime's mechanism the daemon's behaviour.
func TestWireScheduler_ReadsTheLiveConfiguration(t *testing.T) {
	root := bootstrapProject(t)
	cfg := config.Defaults()
	opts := daemon.NewOptions(root, cfg)
	opts.Log = logging.Nop()
	_, schedOpts := wireScheduler(&opts, noEnv, nil)
	require.NotNil(t, schedOpts.CfgFn, "the scheduler must read the live configuration")
	require.Equal(t, opts.CurrentCfg(), schedOpts.CfgFn())
}

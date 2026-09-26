package e2e

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/daemon"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/rehydrate"
)

// TestE2E_SessionStartCompactUnreadableCheckpointStore is owner decision D11 (2026-09-25) through the
// real binary and the daemon's production wiring: a compaction whose checkpoint store cannot be read
// is answered with the explicit deferred note, naming why, and never with silence — and nothing is
// built on the store. Before D11 the host received the §12.1 probe alone, and the model a compacted
// context with no word of what it lost.
//
// The manifest is broken after the daemon is warm, so the compact start is answered by a running
// daemon rather than by the hook client's own no-answer note, which a cold start on a loaded machine
// can produce instead (DeferredNoAnswer).
func TestE2E_SessionStartCompactUnreadableCheckpointStore(t *testing.T) {
	bin := Build(t)
	p := scProject(t)
	t.Cleanup(func() { e2eShutdownIfReachable(t, p.Root) })
	env := e2eEnv(p)

	scWarmDaemon(t, bin, p, env)
	manifest := paths.Long(paths.ManifestPath(paths.Of(p.Root)))
	require.NoError(t, os.Chmod(manifest, 0o600))
	require.NoError(t, os.WriteFile(manifest, []byte("{not a manifest line\n"), 0o600))

	out := scRunStart(t, bin, env, scStartPayload(t, p.Root, "compact", ""))
	ac := scAdditionalContext(t, out)
	require.True(t, strings.HasPrefix(ac, daemon.CompactDeferredNote(scSession, daemon.DeferredCheckpointUnreadable)),
		"an unreadable checkpoint store is answered with the note naming why:\n%s", ac)
	require.Contains(t, ac, scProbePrefix, "the §12.1 probe still follows the note")
	require.NotContains(t, ac, checkpoint.InjectionCloseTag, "nothing is built on a store that cannot be read")
	require.Empty(t, hookio.HostCapOverruns(out), "every field the host receives stays under its cap")

	// The drop report is written after the answer, on work Stop joins: once the daemon is down it is
	// on disk, and dropped() reads it.
	e2eShutdownIfReachable(t, p.Root)
	drops, err := rehydrate.NewReporter(p.Root, logging.Nop()).CurrentDrops(t.Context(), scSession)
	require.NoError(t, err)
	require.NotEmpty(t, drops, "dropped() must describe this compaction")
	require.Equal(t, "rehydration", drops[0].Kind)
	require.True(t, strings.HasPrefix(drops[0].Detail, "not delivered: the checkpoint store could not be read"),
		"dropped() says the rehydration was never built, and why: %q", drops[0].Detail)
}

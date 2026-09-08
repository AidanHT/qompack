package cli

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/daemon"
)

// This is an unavailable-route assertion on real repository wiring. It must be replaced by
// accepted-source/capability evidence when C-1 is enabled, not reused as a recovery pass.
func TestWireSchedulerKeepsFrontierUnavailableUntilSharedSources(t *testing.T) {
	root := t.TempDir()
	opts := daemon.NewOptions(root, config.Defaults())
	t.Cleanup(func() {
		if opts.Store != nil {
			_ = opts.Store.Close()
		}
		if opts.Ledger != nil {
			_ = opts.Ledger.Close()
		}
	})
	_, err := daemon.WireObserver(&opts)
	require.NoError(t, err)
	sched, schedOpts := wireScheduler(&opts, nil)
	require.NotNil(t, sched, "local scheduling still constructs")
	t.Cleanup(func() { closeScheduler(sched, schedOpts) })
	require.Nil(t, schedOpts.Frontier)
	require.Nil(t, schedOpts.Sources)
	require.NoFileExists(t, filepath.Join(root, ".qompack", "records", "eliminations.jsonl"))
}

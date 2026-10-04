package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/paths"
)

// TestCheckoutSpoolGuard_FailsOnAStoreTheRunCreated pins TestMain's guard (w20 status review
// round 2 nit). When the checkout had no store before the run, any .qompack the run leaves behind
// fails it, whatever wrote it: an externalized blob, a log, a daemon spawned at the checkout root.
// When a store was already there (a real session may be using it), only this process's own client
// spool is watched, so another process's hooks can never trip it.
func TestCheckoutSpoolGuard_FailsOnAStoreTheRunCreated(t *testing.T) {
	t.Run("no store before, a log after", func(t *testing.T) {
		root := t.TempDir()
		g, err := newCheckoutSpoolGuardAt(root)
		require.NoError(t, err)
		require.NoError(t, g.check(), "nothing was written")

		logs := filepath.Join(paths.Of(root).Dot, "logs")
		require.NoError(t, os.MkdirAll(logs, 0o700))
		require.NoError(t, os.WriteFile(filepath.Join(logs, "daemon.log"), []byte("x"), 0o600))
		err = g.check()
		require.Error(t, err, "the run created the checkout's store")
		require.Contains(t, err.Error(), "logs/daemon.log", "it names what is inside")
	})

	t.Run("a store before, another process's spool after", func(t *testing.T) {
		root := t.TempDir()
		spool := paths.Of(root).Spool
		require.NoError(t, os.MkdirAll(spool, 0o700))
		g, err := newCheckoutSpoolGuardAt(root)
		require.NoError(t, err)

		other := filepath.Join(spool, fmt.Sprintf("client-%d.ndjson", os.Getpid()+1))
		require.NoError(t, os.WriteFile(other, []byte("{}\n"), 0o600))
		require.NoError(t, g.check(), "another process's hooks may write a store that already existed")

		own := filepath.Join(spool, fmt.Sprintf("client-%d.ndjson", os.Getpid()))
		require.NoError(t, os.WriteFile(own, []byte("{}\n"), 0o600))
		require.Error(t, g.check(), "this process's own spool grew")
	})
}

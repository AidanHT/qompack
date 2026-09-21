//go:build linux || darwin

package store

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/paths"
)

func TestObservationGuards_FIFOAndAliasedIndexRefused(t *testing.T) {
	for _, mode := range []string{"fifo", "aliased index"} {
		t.Run(mode, func(t *testing.T) {
			tp := newTestStore(t)
			require.NoError(t, tp.Store.Close())
			if mode == "fifo" {
				require.NoError(t, syscall.Mkfifo(obsPath(tp.Root), 0o600))
			} else {
				index := paths.Of(tp.Root).Index
				require.NoError(t, os.Rename(index, index+"-original"))
				destination := t.TempDir()
				require.NoError(t, os.WriteFile(filepath.Join(destination, observationsFile), []byte("external sentinel"), 0o600))
				require.NoError(t, os.Symlink(destination, index))
			}
			// This isolates the observation reader; it is not a claim that every
			// legacy index reader is confined. The command timeout also catches
			// the original FIFO hang (no writer ever opens this named pipe).
			tp.Store.mu.Lock()
			tp.Store.loadObservationsLocked()
			uncertain := tp.Store.obsSidecarUncertain
			tp.Store.mu.Unlock()
			require.True(t, uncertain)
		})
	}
}

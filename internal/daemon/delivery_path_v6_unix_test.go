//go:build unix

package daemon

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDeliveryPath_V6_StaticFIFOsRefusedBeforeOpen(t *testing.T) {
	for _, kind := range []string{"radix-page", "generation-log", "segment-log"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			var operation func() error
			switch kind {
			case "radix-page":
				r, err := openDeliveryRadix(dir)
				require.NoError(t, err)
				t.Cleanup(func() { _ = r.close() })
				h := radixHash{1}
				require.NoError(t, os.Mkdir(filepath.Join(dir, r.shard(h)), 0o700))
				require.NoError(t, syscall.Mkfifo(r.pagePath(h), 0o600))
				operation = func() error { _, err := r.readPage(h); return err }
			case "generation-log":
				require.NoError(t, syscall.Mkfifo(filepath.Join(dir, genLogFile), 0o600))
				operation = func() error {
					g, err := openDeliveryGenerations(dir)
					if g != nil {
						_ = g.close()
					}
					return err
				}
			case "segment-log":
				require.NoError(t, syscall.Mkfifo(filepath.Join(dir, deliverySegmentLogFile), 0o600))
				operation = func() error {
					s, _, err := openDeliverySegments(dir)
					if s != nil {
						_ = s.close()
					}
					return err
				}
			}
			finished := make(chan error, 1)
			go func() { finished <- operation() }()
			select {
			case err := <-finished:
				require.Error(t, err)
			case <-hangGuard(t):
				t.Fatal("a static FIFO must be rejected without waiting for a peer")
			}
		})
	}
}

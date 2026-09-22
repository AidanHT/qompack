//go:build unix

package store

import (
	"context"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/paths"
)

// TestGCRetention_FifoAckSourceDoesNotHang (unix only): a FIFO planted where delivery-acks.jsonl
// belongs must be REJECTED before any open — a plain os.Open of a reader-less FIFO blocks forever, so
// the unfixed reader would hang the whole GC pass. The confined Lstat rejects the non-regular entry,
// so GC reads no acknowledgements (every lease stays open and retained) and completes promptly. The
// watchdog fails the test if the pass ever blocks on the pipe.
func TestGCRetention_FifoAckSourceDoesNotHang(t *testing.T) {
	tp := newTestStore(t)
	ctx := context.Background()
	l := paths.Of(tp.Root)
	leased := gcSeed(t, tp, "src/leased.txt", "held by an open lease; the ack journal is a FIFO\n")
	nonce := deliveryNonce("f")
	writeJSONLLines(t, l.State, deliveryLeaseFile,
		map[string]any{"v": 1, "delivery": nonce, "request": leased.Hash.String(), "observation_id": obsIDText("fifo")})
	require.NoError(t, syscall.Mkfifo(filepath.Join(l.State, deliveryAckFile), 0o600))

	type result struct {
		rep GCReport
		err error
	}
	done := make(chan result, 1)
	go func() {
		rep, err := tp.Store.GC(ctx, forceCollect)
		done <- result{rep, err}
	}()

	select {
	case r := <-done:
		require.NoError(t, r.err)
		require.False(t, r.rep.RetentionRootsError, "a FIFO ack journal retains more, it does not halt")
		_, err := tp.Store.GetRoot(ctx, leased.Hash)
		require.NoError(t, err, "the lease stays open when its acknowledgements cannot be read")
	case <-time.After(15 * time.Second):
		t.Fatal("GC hung: a FIFO where delivery-acks.jsonl belongs was opened with a blocking read")
	}
}

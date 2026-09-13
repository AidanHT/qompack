package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/paths"
)

// The pin below closes the one gap this round's own target set left (SP20-D1, adversarial review
// round 2, R2-3). drainFileRecord replaced the field tags drainFileState used to carry with a
// hand-written MarshalJSON/UnmarshalJSON pair, and a hand-written codec's characteristic failure is
// dropping a field silently. The bound and provenance pins beside this file assert Size, Offset,
// Done and the durable-size mark through that codec; PendingBlobs they never name, so a mutant that
// drops it from either half survives every one of them and is caught only by the whole package.
//
// What the field costs if it is dropped is not cosmetic. PendingBlobs is the only durable record of
// a blob file whose line has already been acknowledged: the line is behind the consumed offset, so
// no later pass reads it again, and the descriptor naming the blob is never scanned again either. A
// codec that loses the intent therefore leaks that blob permanently — nothing lists it, nothing
// references it, and no pass knows to remove it — which is exactly the failure the intent is
// persisted before any deletion to prevent.

// TestDrainRoundTripsACleanupIntentThroughTheRecordCodec pins PendingBlobs across both halves of the
// codec, each half on its own: the first case writes the record with saveState and reads the bytes
// that reach disk, so it turns on MarshalJSON; the second seeds those bytes by hand and makes a pass
// act on them, so it turns on UnmarshalJSON. Both are deterministic — no goroutine, no clock, no
// sleep, no wall-clock assertion.
func TestDrainRoundTripsACleanupIntentThroughTheRecordCodec(t *testing.T) {
	t.Run("the intents a record holds survive the write and the read", func(t *testing.T) {
		root := t.TempDir()
		const base = "client-4747.ndjson"
		size := int64(len(wireLine(t, ipc.Request{Op: ipc.OpObserveTool, Session: "sess-codec", TS: 1}).line))
		want := []string{"blob-4747-0.bin", "blob-4747-1.bin"}

		require.NoError(t, newDrainer(DrainConfig{Root: root}).saveState(drainState{
			base: &drainFileState{Size: size, Offset: size, Done: true, PendingBlobs: want},
		}))

		// In the bytes, under the on-disk field name a restarted process reads, and in the order the
		// pass appended them: cleanupAcknowledged walks the slice, and a reordering of two intents is
		// still two blobs it must try in a defined order.
		b, err := os.ReadFile(paths.Long(drainStatePath(root)))
		require.NoError(t, err)
		var raw map[string]map[string]any
		require.NoError(t, json.Unmarshal(b, &raw))
		require.Equal(t, []any{"blob-4747-0.bin", "blob-4747-1.bin"}, raw[base]["pending_blobs"],
			"MarshalJSON must write every cleanup intent the record holds, in order")

		// And back out through the loader that restarted process uses.
		require.Equal(t, &drainFileState{Size: size, Offset: size, Done: true, PendingBlobs: want},
			diskDrainState(t, root)[base],
			"UnmarshalJSON must read back every cleanup intent the record carries")
	})

	t.Run("a later pass removes the blob an inherited intent names", func(t *testing.T) {
		ctx := context.Background()
		root := t.TempDir()
		spool := paths.Of(root).Spool
		require.NoError(t, os.MkdirAll(paths.Long(spool), 0o700))

		const blobName = "blob-4646-0.bin"
		blobPath := filepath.Join(spool, blobName)
		require.NoError(t, os.WriteFile(paths.Long(blobPath), []byte(`{"externalized":true}`), 0o600))

		// A file an earlier pass drained to its end, whose own bytes reference no blob. Nothing in
		// the spool names the blob any more, so the record's intent is the only thing that does.
		const base = "client-4646.ndjson"
		path := filepath.Join(spool, base)
		require.NoError(t, os.WriteFile(paths.Long(path),
			wireLine(t, ipc.Request{Op: ipc.OpObserveTool, Session: "sess-codec", TS: 1}).line, 0o600))
		size := spoolFileSize(t, path)

		// Seeded in the bytes rather than through saveState, so this case turns on the read half
		// alone: what MarshalJSON would have written is not what the pass is given here.
		seeded, err := json.Marshal(map[string]map[string]any{
			base: {
				"size": size, "offset": size, "done": true, "durable_size": true,
				"pending_blobs": []string{blobName},
			},
		})
		require.NoError(t, err)
		statePath := drainStatePath(root)
		require.NoError(t, os.MkdirAll(paths.Long(filepath.Dir(statePath)), 0o700))
		require.NoError(t, os.WriteFile(paths.Long(statePath), seeded, 0o600))
		require.Equal(t, []string{blobName}, diskDrainState(t, root)[base].PendingBlobs,
			"fixture: the seeded record really does carry the intent")

		var got []core.UnixMilli
		dr := newDrainer(DrainConfig{
			Root: root, Log: newRecordingLogger(), Clock: newFakeClock(epoch),
			Dispatch: func(_ context.Context, r ipc.Request) ipc.Response {
				got = append(got, r.TS)
				return ipc.Response{OK: true}
			},
		})

		n, err := dr.Drain(ctx)
		require.NoError(t, err)
		require.Zero(t, n, "fixture: the record already names the whole file as consumed")
		require.Empty(t, got, "so nothing is dispatched again")
		require.NoFileExists(t, blobPath,
			"the intent the record carried is the only authorization to remove the blob: dropped, the blob leaks")
		require.NoFileExists(t, path, "and the drained file is released once its last intent is consumed")
		require.Empty(t, diskDrainState(t, root), "with the entry that held it")
	})
}

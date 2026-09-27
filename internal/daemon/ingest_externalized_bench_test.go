package daemon

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/paths"
)

// BenchmarkIngestAcceptExternalized prices the externalized-payload barrier (w6-ckptsync, 1a4311f):
// an Accept whose tool response the hook moved to spool/blob-<pid>-<n>.bin, which the ingest now
// syncs — the blob's bytes, then the spool directory — before the WAL line that names it. It is
// BenchmarkIngestAccept (the unleased control, which pays only the WAL fsync) with that descriptor on
// every request, so the difference between the two is the barrier.
//
// Each iteration's blob is written with the timer stopped, as the hook writes it (unsynced), and
// removed afterwards, also untimed, so the disk holds one blob at a time. Its size is ipc.MaxLineBytes:
// the client externalizes a request once its line reaches the frame limit (or the configured
// maxPayloadBytes, which defaults to the same 1 MiB), so this is the smallest payload that takes the
// path and the barrier's cost grows with the payload from here. ns/op is Accept alone; it is a
// shape/regression figure read as a ratio against its own base, never a budget.
func BenchmarkIngestAcceptExternalized(b *testing.B) {
	root := b.TempDir()
	ing := newIngest(root, config.Defaults(), logging.Nop(), nil, core.SystemClock())
	b.Cleanup(func() { _ = ing.Close() })
	spool := paths.Of(root).Spool
	if err := os.MkdirAll(paths.Long(spool), 0o700); err != nil {
		b.Fatal(err)
	}
	payload := make([]byte, ipc.MaxLineBytes)
	for i := range payload {
		payload[i] = byte('a' + i%26)
	}

	req := func(i int) (ipc.Request, []byte, string) {
		name := fmt.Sprintf("%s%d-%d.bin", blobFilePrefix, os.Getpid(), i)
		ref, err := json.Marshal(blobRef{Blob: name, Bytes: len(payload), Field: drainBlobToolResponse})
		if err != nil {
			b.Fatal(err)
		}
		r := ipc.Request{Op: ipc.OpObserveTool, Session: "bench-externalized", Event: &hookio.Event{}, Raw: ref}
		line, err := ipc.EncodeRequest(r)
		if err != nil {
			b.Fatal(err)
		}
		p := filepath.Join(spool, name)
		if err := os.WriteFile(paths.Long(p), payload, 0o600); err != nil {
			b.Fatal(err)
		}
		return r, line, p
	}

	// Warm the WAL handle (its segment open and the spool directory's sync) before timing.
	r, line, p := req(-1)
	if err := ing.Accept(r, line); err != nil {
		b.Fatal(err)
	}
	_ = os.Remove(paths.Long(p))

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		r, line, p := req(i)
		b.StartTimer()
		if err := ing.Accept(r, line); err != nil {
			b.Fatal(err)
		}
		b.StopTimer()
		_ = os.Remove(paths.Long(p))
		b.StartTimer()
	}
}

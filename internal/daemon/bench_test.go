package daemon

import (
	"testing"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/logging"
)

// BenchmarkIngestAccept measures Accept against a warm WAL handle (task-3-spec.md's B-B row: p99 <
// 2ms). It is a shape/regression check, not a CI gate — the real gate is
// devtool bench-hotpath (SP-05's later commits).
func BenchmarkIngestAccept(b *testing.B) {
	root := b.TempDir()
	clk := core.SystemClock()
	ing := newIngest(root, config.Defaults(), logging.Nop(), nil, clk)
	b.Cleanup(func() { _ = ing.Close() })

	req := ipc.Request{Op: ipc.OpObserveTool, Session: "bench-sess"}
	line := []byte(`{"op":"observe.tool","s":"bench-sess","t":1}`)

	// Warm the WAL handle before timing.
	if err := ing.Accept(req, line); err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := ing.Accept(req, line); err != nil {
			b.Fatal(err)
		}
	}
}

// benchLeasedBaseTS is the host timestamp the first leased request carries; every later iteration
// adds its index so deliveryRequestHash binds each lease to a distinct request, the way one hook
// invocation per millisecond would. A benchmark input, not a configuration value.
const benchLeasedBaseTS = core.UnixMilli(1_700_000_000_000)

// BenchmarkIngestAcceptLeased is the evidence benchmark for carried defect SP20-D1 (V5-VERIFY:
// budget B-B, l0_ingest p99 < 2 ms, against the leased Accept's durability points). It is
// BenchmarkIngestAccept — the unleased control, which pays only the WAL fsync — with the two
// things the production hot path adds since fix(ipc) 9c023ac widened the delivery nonce to the
// 64-hex token the journal accepts: every request carries a fresh nonce, and ing.journal is the
// real file-backed deliveryJournal held under the singleton Lock, wired the way daemon.New wires
// it (daemon.go: d.ing.journal = d.deliveryJournal). Each iteration is therefore one accepted
// LEASED delivery and pays the four fsync syscalls Accept's doc comment audits, in this order:
//
//  1. appendWAL: the session WAL segment's Sync (ingest.go), under ingest.mu;
//  2. deliveryJournal.lease: the lease journal's Sync (delivery_lease.go). Part 1 moved this off
//     Lock.mu — a lease batch commits between the journal's enter and leave and takes Lock.mu
//     nowhere — so an isolated iteration pays the sync without holding a lock;
//  3. deliveryJournal.savePosition -> paths.WriteAtomic: the position sidecar's temp-file Sync;
//  4. paths.WriteAtomic's parent-directory fsync (fsyncDir) — a real syscall on POSIX, a no-op on
//     Windows (atomic.go), so a Windows run of this benchmark pays three FlushFileBuffers, not four.
//
// There is no cheap counting seam across all four (the WAL and the WriteAtomic staging file are
// bare *os.File handles), so the count is stated here rather than reported as a metric; the
// benchmark instead proves every timed iteration really took a lease by checking the journal's
// in-memory assignment count afterwards, because an unleased fallback is silent (Accept counts it
// and moves on) and would quietly turn this back into the control.
//
// ns/op here is the per-call service time of one Accept on an idle daemon: no queueing, no
// transport. Read against B-B's sample (devtool bench-hotpath), which times Accept INCLUDING the
// wait for ingest.mu behind the previous hook's Accept, the difference is the queue. Whether the
// budget, the durability points, or the harness changes is not this benchmark's call: the
// decision belongs to the owner named in the SP20-D1 row of CARRIED-DEFECTS.tsv.
func BenchmarkIngestAcceptLeased(b *testing.B) {
	root := b.TempDir()
	clk := core.SystemClock()
	ing := newIngest(root, config.Defaults(), logging.Nop(), nil, clk)
	b.Cleanup(func() { _ = ing.Close() })

	lock, err := acquireTestDeliveryLock(root)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = lock.Release() })
	ing.journal = lock.openDeliveryJournal

	// One request per iteration plus one for the warm call, each with its own production-shape
	// nonce and its own host timestamp, encoded exactly as acceptHotPathEvent hands the line to
	// Accept (handlers.go: ipc.EncodeRequest, terminator included). Minting and encoding happen
	// here, outside the timed region, so ns/op is Accept alone.
	total := b.N + 1
	reqs := make([]ipc.Request, total)
	lines := make([][]byte, total)
	for i := range reqs {
		nonce, err := ipc.NewDeliveryNonce()
		if err != nil {
			b.Fatal(err)
		}
		reqs[i] = ipc.Request{
			Op: ipc.OpObserveTool, Session: "bench-sess", TS: benchLeasedBaseTS + core.UnixMilli(i), Nonce: nonce,
		}
		if lines[i], err = ipc.EncodeRequest(reqs[i]); err != nil {
			b.Fatal(err)
		}
	}

	// Warm the WAL handle, the lock's journal and the position sidecar before timing.
	if err := ing.Accept(reqs[b.N], lines[b.N]); err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := ing.Accept(reqs[i], lines[i]); err != nil {
			b.Fatal(err)
		}
	}
	b.StopTimer()

	journal, err := lock.openDeliveryJournal()
	if err != nil {
		b.Fatal(err)
	}
	if got := len(journal.leases); got != total {
		b.Fatalf("leased %d of %d deliveries: the timed loop fell back to the unleased path", got, total)
	}
}

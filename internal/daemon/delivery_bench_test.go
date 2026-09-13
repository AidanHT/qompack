package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"io"
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/logging"
)

// The SP20-D1 baseline (step M0 of its design): benchmarks of the leased delivery path that run on
// the code as it stands, so the same rows can be measured on a quiet window before and after the
// durable path is reworked. BenchmarkIngestAccept and BenchmarkIngestAcceptLeased (bench_test.go)
// are the isolated rows and are left exactly as they are; everything here is additive.

// benchBurstDeliveries is the burst size the SP20-D1 design names: 2 000 deliveries, the iteration
// count bench-hotpath's gate runs.
const benchBurstDeliveries = 2000

// benchBurstFlag lowers the burst size for a smoke run on a loaded machine. The sub-benchmark names
// carry the size actually run, so a reduced run can never be mistaken for the 2 000-delivery row.
var benchBurstFlag = flag.Int("daemon.burst", benchBurstDeliveries,
	"deliveries per BenchmarkIngestAcceptLeasedBurst burst")

// leasedBench is one leased ingest wired the way daemon.New wires it (d.ing.journal =
// d.deliveryJournal): a real file-backed deliveryJournal held under the singleton Lock, every
// session's WAL handle and the journal's position sidecar already warm, and one request per timed
// delivery minted and encoded up front, exactly as acceptHotPathEvent hands a line to Accept, so
// nonce minting and encoding stay outside the timed region.
type leasedBench struct {
	ing     *ingest
	lock    *Lock
	journal *deliveryJournal
	reqs    []ipc.Request
	lines   [][]byte
	warm    int // reqs[:warm] were accepted before timing, one per session
}

func newLeasedBench(b *testing.B, sessions, deliveries int) *leasedBench {
	b.Helper()
	return newLeasedBenchFormat(b, sessions, deliveries, deliverySealWriteFormat)
}

// newLeasedBenchFormat is newLeasedBench with the seal format its journal WRITES chosen explicitly,
// through the lock's own seam (lock.go, sealFormat). The §6.3 rows that time the v2 seal need a
// journal that holds its A/B file open; everything else here is the production path, as above.
func newLeasedBenchFormat(b *testing.B, sessions, deliveries, format int) *leasedBench {
	b.Helper()
	root := b.TempDir()
	ing := newIngest(root, config.Defaults(), logging.Nop(), nil, core.SystemClock())
	b.Cleanup(func() { _ = ing.Close() })
	lock, err := acquireTestDeliveryLock(root)
	if err != nil {
		b.Fatal(err)
	}
	lock.sealFormat = format
	b.Cleanup(func() { _ = lock.Release() })
	ing.journal = lock.openDeliveryJournal

	lb := &leasedBench{ing: ing, lock: lock, warm: sessions}
	lb.reqs = make([]ipc.Request, sessions+deliveries)
	lb.lines = make([][]byte, len(lb.reqs))
	for k := range lb.reqs {
		nonce, err := ipc.NewDeliveryNonce()
		if err != nil {
			b.Fatal(err)
		}
		lb.reqs[k] = ipc.Request{
			Op: ipc.OpObserveTool, Session: benchSession(k%sessions, sessions),
			TS: benchLeasedBaseTS + core.UnixMilli(k), Nonce: nonce,
		}
		if lb.lines[k], err = ipc.EncodeRequest(lb.reqs[k]); err != nil {
			b.Fatal(err)
		}
	}
	for k := range sessions {
		if err := lb.accept(k); err != nil {
			b.Fatal(err)
		}
	}
	lb.journal = lb.requireLeased(b, sessions)
	return lb
}

// benchSession names session k of sessions. A single-session bench keeps the name
// BenchmarkIngestAcceptLeased uses.
func benchSession(k, sessions int) core.SessionID {
	if sessions == 1 {
		return "bench-sess"
	}
	return core.SessionID("bench-sess-" + strconv.Itoa(k))
}

// accept delivers reqs[k] through the production Accept.
func (lb *leasedBench) accept(k int) error { return lb.ing.Accept(lb.reqs[k], lb.lines[k]) }

// requireLeased fails unless the journal is still open and unfaulted and holds exactly want leases.
// Accept's unleased fallback is silent — it counts a gap and returns nil — so without this a
// benchmark whose journal broke would quietly measure the unleased control instead. Call it only
// once every goroutine that touched the journal has been joined.
func (lb *leasedBench) requireLeased(b *testing.B, want int) *deliveryJournal {
	b.Helper()
	journal, err := lb.lock.openDeliveryJournal()
	if err != nil {
		b.Fatal(err)
	}
	if got := len(journal.leases); got != want {
		b.Fatalf("leased %d of %d deliveries: an Accept fell back to the unleased path", got, want)
	}
	return journal
}

// checkFile runs the journal's own checkFile under Lock.mu, as this benchmark always has. A lease
// batch runs it without Lock.mu, between the journal's enter and leave; no batch is in flight while
// a component row runs.
func (lb *leasedBench) checkFile() error {
	lb.lock.mu.Lock()
	defer lb.lock.mu.Unlock()
	return lb.journal.checkFile()
}

// reseal runs the journal's own savePosition under Lock.mu with the position the journal already
// holds, so the seal is a full paths.WriteAtomic and the journal stays consistent with it.
func (lb *leasedBench) reseal() error {
	lb.lock.mu.Lock()
	defer lb.lock.mu.Unlock()
	j := lb.journal
	return j.savePosition(j.bytes, len(j.leases), j.chain)
}

// writeSync appends line through the lease journal's own writer and syncs it, under Lock.mu, as a
// lease batch does (without Lock.mu) between its checkFile and its seal. It deliberately neither
// seals nor admits the line, so it leaves the journal ahead of its position: only the
// journalWriteSync row calls it, on a root nothing reopens.
func (lb *leasedBench) writeSync(line []byte) error {
	lb.lock.mu.Lock()
	defer lb.lock.mu.Unlock()
	n, err := lb.journal.writer.Write(line)
	if err != nil {
		return err
	}
	if n != len(line) {
		return io.ErrShortWrite
	}
	return lb.journal.writer.Sync()
}

// BenchmarkDeliveryLeaseComponents is SP20-D1's step M0. It times, one at a time on an idle
// journal, each piece of today's leased Accept, so that the part of a leased Accept which its three
// flushes do not explain is attributed by measurement rather than inferred. The rows are:
//
//   - accessorOwned: ing.journal(), that is Lock.openDeliveryJournal's fast path. Before the lease
//     stage it was Lock.mu plus an owned() lock-file read on every call; with O1 it is Lock.mu and
//     the open journal's in-memory checks.
//
//   - owned: Lock.owned under Lock.mu, the lock-file read the accessor, and then deliveryJournal.lease
//     a second time, made on every leased Accept before the lease stage.
//
//   - ownedByFile: Lock.ownedByFile, the one lock-file read a lease batch makes, once per batch and
//     without Lock.mu.
//
//   - checkFileV1: deliveryJournal.checkFile against a position sidecar untouched since its seal.
//
//   - checkFileV1AfterSeal: the same check immediately after the seal it checks, the case where
//     the sidecar was created by a rename moments earlier; the seal runs with the timer stopped.
//
//   - journalWriteSync: one canonical lease line written and synced on the journal's own handle.
//
//   - sealWriteAtomic: deliveryJournal.savePosition, the paths.WriteAtomic seal.
//
//   - walWriteSync: ingest.appendWAL, one WAL line written and synced under ingest.mu.
//
//   - lease: deliveryJournal.lease end to end with a fresh nonce per iteration, each a lease batch
//     of one. The pieces should add up to it (ownedByFile + checkFileV1* + journalWriteSync +
//     sealWriteAtomic + marshal, chain and queue bookkeeping), and walWriteSync + accessorOwned +
//     lease should add up to BenchmarkIngestAcceptLeased.
//
//   - checkFileV2: the same per-batch check against a journal whose seal is the HELD v2 A/B file,
//     which is not reopened by path at all: an Lstat, a SameFile and one cached 32 KiB ReadAt.
//
//   - sealSlot: one v2 seal through that held handle — the slot's WriteAt, its SyncData and the
//     post-seal identity check. It is sealWriteAtomic's replacement, and the two beside each other
//     are the measurement design §7.2's step-2 prediction rests on.
//
//   - postSealIdentity: that seal's identity check alone (an Lstat and a SameFile), which is what an
//     in-place write pays for the guarantee a rename gives v1 for free (J-B5).
//
// Every format-sensitive row names its format through the lock's own seam rather than inheriting
// deliverySealWriteFormat, so one run prices both formats of the same step whichever one the build
// writes: checkFileV1, checkFileV1AfterSeal and sealWriteAtomic at format 1, checkFileV2, sealSlot
// and postSealIdentity at format 2. Since the step-2 flip the build writes format 2, and a row that
// followed the constant would have priced the held seal under a name that says WriteAtomic.
//
// The rows that are not about the seal at all — the ownership reads, journalWriteSync and
// walWriteSync — do follow the build, because what they measure is the same in either format and
// following it keeps them honest about the binary being shipped.
func BenchmarkDeliveryLeaseComponents(b *testing.B) {
	b.Run("accessorOwned", func(b *testing.B) {
		lb := newLeasedBench(b, 1, 0)
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			if _, err := lb.lock.openDeliveryJournal(); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("owned", func(b *testing.B) {
		lb := newLeasedBench(b, 1, 0)
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			lb.lock.mu.Lock()
			ok := lb.lock.owned()
			lb.lock.mu.Unlock()
			if !ok {
				b.Fatal("the benchmark's own lock reads as not owned")
			}
		}
	})
	b.Run("ownedByFile", func(b *testing.B) {
		lb := newLeasedBench(b, 1, 0)
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			if !lb.lock.ownedByFile() {
				b.Fatal("the benchmark's own lock reads as not owned")
			}
		}
	})
	b.Run("checkFileV1", func(b *testing.B) {
		lb := newLeasedBenchFormat(b, 1, 0, 1)
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			if err := lb.checkFile(); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("checkFileV1AfterSeal", func(b *testing.B) {
		lb := newLeasedBenchFormat(b, 1, 0, 1)
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			b.StopTimer()
			if err := lb.reseal(); err != nil {
				b.Fatal(err)
			}
			b.StartTimer()
			if err := lb.checkFile(); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("checkFileV2", func(b *testing.B) {
		lb := newLeasedBenchFormat(b, 1, 0, 2)
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			if err := lb.checkFile(); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("journalWriteSync", func(b *testing.B) {
		lb := newLeasedBench(b, 1, 0)
		line, err := json.Marshal(lb.journal.leases[lb.reqs[0].Nonce])
		if err != nil {
			b.Fatal(err)
		}
		line = append(line, '\n')
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			if err := lb.writeSync(line); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("sealWriteAtomic", func(b *testing.B) {
		lb := newLeasedBenchFormat(b, 1, 0, 1)
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			if err := lb.reseal(); err != nil {
				b.Fatal(err)
			}
		}
		b.StopTimer()
		if err := lb.checkFile(); err != nil {
			b.Fatal(err) // every rewritten seal must still describe the journal
		}
	})
	b.Run("sealSlot", func(b *testing.B) {
		lb := newLeasedBenchFormat(b, 1, 0, 2)
		seal := lb.journal.seal
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			// Each seal must strictly extend the one before it, as consecutive seals do; a seal that
			// reaches the journal's entry cap is replaced with the timer stopped, exactly as
			// BenchmarkDeliverySealWrite does it.
			if seal.cur.Count == deliveryLeaseMaxEntries {
				b.StopTimer()
				lb = newLeasedBenchFormat(b, 1, 0, 2)
				seal = lb.journal.seal
				b.StartTimer()
			}
			// This deliberately leaves the seal ahead of the journal beside it, the mirror of what
			// the journalWriteSync row leaves behind: only these rows touch this root, and nothing
			// reopens it.
			if err := seal.write(seal.cur.Bytes+1, seal.cur.Count+1, seal.cur.Chain); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("postSealIdentity", func(b *testing.B) {
		lb := newLeasedBenchFormat(b, 1, 0, 2)
		seal := lb.journal.seal
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			if err := seal.verifyIdentity(); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("walWriteSync", func(b *testing.B) {
		lb := newLeasedBench(b, 1, 0)
		line := bytes.TrimSuffix(lb.lines[0], []byte{'\n'})
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			if err := lb.ing.appendWAL(lb.reqs[0].Session, line); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("lease", func(b *testing.B) {
		lb := newLeasedBench(b, 1, b.N)
		ctx := context.Background()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			req := lb.reqs[lb.warm+i]
			if _, err := lb.journal.lease(ctx, req.Nonce, req.Session, deliveryRequestHash(req)); err != nil {
				b.Fatal(err)
			}
		}
		b.StopTimer()
		lb.requireLeased(b, len(lb.reqs))
	})
}

// BenchmarkIngestAcceptLeasedParallel is BenchmarkIngestAcceptLeased with concurrent callers:
// fresh nonces, one request per delivery minted up front, the same leases-held assertion.
//
// The first name level is the number of leased Accepts in flight at once, enforced by a semaphore
// held around each call; b.RunParallel starts at least GOMAXPROCS goroutines, so its own
// parallelism cannot express 1 or 4. The second level is the number of sessions the deliveries
// rotate over: one WAL segment, or four. ns/op — and the same figure as ms/op — is wall time per
// delivery, the inverse of throughput. accept-ms/op is the mean latency of one Accept call measured
// around the call, which grows with the queue in front of the durable path.
//
// syncs/op is the durability points one delivery pays, counted through the seams
// (countLeasedDurability): WAL Syncs, lease-journal Syncs and seals. lease-batches/op is the lease
// batches per delivery. An isolated delivery pays 3 syncs and one batch; deliveries in flight
// together share both.
func BenchmarkIngestAcceptLeasedParallel(b *testing.B) {
	for _, inFlight := range []int{1, 4, 16, 64} {
		b.Run(strconv.Itoa(inFlight), func(b *testing.B) {
			for _, sessions := range []int{1, 4} {
				b.Run("sessions-"+strconv.Itoa(sessions), func(b *testing.B) {
					benchLeasedParallel(b, inFlight, sessions)
				})
			}
		})
	}
}

func benchLeasedParallel(b *testing.B, inFlight, sessions int) {
	lb := newLeasedBench(b, sessions, b.N)
	syncs, batches := countLeasedDurability(lb)
	slots := make(chan struct{}, inFlight)
	var next, acceptNs atomic.Int64
	procs := runtime.GOMAXPROCS(0)
	b.SetParallelism((inFlight + procs - 1) / procs)
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			k := lb.warm + int(next.Add(1)-1)
			slots <- struct{}{}
			start := time.Now()
			err := lb.accept(k)
			acceptNs.Add(int64(time.Since(start)))
			<-slots
			if err != nil {
				b.Error(err)
				return
			}
		}
	})
	b.StopTimer()
	lb.requireLeased(b, len(lb.reqs))
	b.ReportMetric(benchMillis(b.Elapsed())/float64(b.N), "ms/op")
	b.ReportMetric(benchMillis(time.Duration(acceptNs.Load()))/float64(b.N), "accept-ms/op")
	b.ReportMetric(float64(syncs.Load())/float64(b.N), "syncs/op")
	b.ReportMetric(float64(batches.Load())/float64(b.N), "lease-batches/op")
}

// countLeasedDurability wraps lb's durability seams so that a benchmark can report what a delivery
// costs in durability points: the WAL's Sync (ingest.syncWAL), and the lease journal's writer and
// seal (deliveryJournal.writer, sealLease). Every WAL Sync, journal Sync and seal counts into
// syncs, a seal as one point however many syscalls paths.WriteAtomic makes, and every journal
// Write into batches, since a lease batch that mints makes exactly one. Call it after warm-up and
// before any concurrent Accept.
func countLeasedDurability(lb *leasedBench) (syncs, batches *atomic.Int64) {
	syncs, batches = new(atomic.Int64), new(atomic.Int64)
	walSync := lb.ing.syncWAL
	lb.ing.syncWAL = func(f *os.File) error {
		syncs.Add(1)
		return walSync(f)
	}
	j := lb.journal
	f := j.file
	j.writer = leaseFaultWriter{
		file: f,
		write: func(p []byte) (int, error) {
			batches.Add(1)
			return f.Write(p)
		},
		sync: func() error {
			syncs.Add(1)
			return f.Sync()
		},
	}
	seal := j.sealLease
	j.sealLease = func(size int64, count int, chain core.Hash) error {
		syncs.Add(1)
		return seal(size, count, chain)
	}
	return syncs, batches
}

// BenchmarkIngestAcceptLeasedBurst replays a hook burst against one leased ingest with the B-C
// workers running. It reports each delivery's latency — arrival to Accept returned, the window a
// hook's ACK wait covers — as p50-ms, p99-ms and max-ms over every delivery of every iteration;
// ns/op is one whole burst. Its one variant, simultaneous-N, releases N deliveries together: every
// goroutine is blocked on one channel, and latency is measured from its close.
//
// Open-loop pacing, where deliveries arrive at a fixed interval whether or not the earlier ones
// have returned, is not measured here. Holding each arrival until its time takes a wall-clock
// wait, which 00-ARCHITECTURE.md §6.1 bans outside test/bench, so devtool bench-hotpath measures
// open-loop pacing under test/bench, the one exempt place, which paces real hook processes: at an
// ACK deadline below B-B, each hook it spawns waits the deadline out, spools and exits before the
// next one starts (test/bench/hotpath/process.go).
//
// The B-C side is the production worker pool — ingest.Start with the daemon's worker count — and
// the production dispatch: publishCapture's sidecar WriteAtomic, then the handler, stubbed to
// acknowledge, then commitDelivery's acknowledgement.
//
// That acknowledgement no longer runs under Lock.mu: part 2 gave it a group-commit pipeline of its
// own (ackQ), so its Write, Sync and seal happen between the journal's enter and leave, holding no
// lock a lease needs. Each side still takes Lock.mu once per delivery, in the journal accessor alone
// (Lock.openDeliveryJournal's O1 fast path, which for an open journal is in-memory checks), and
// never across I/O. So what a burst contends for here is the DEVICE — the capture sidecar's
// WriteAtomic beside the lease and acknowledgement batches' own flushes — rather than one mutex
// held across all of them; only the observer's real reference write is left out. After the burst
// the ring is closed, the workers drain it and are joined, and the benchmark fails unless every
// delivery holds a lease and an acknowledgement.
//
// The design also names a paced-closed-loop variant and a with/without-O1 split; they join when
// the accessor change they compare exists. -daemon.burst lowers N for a smoke run.
func BenchmarkIngestAcceptLeasedBurst(b *testing.B) {
	n := *benchBurstFlag
	b.Run("simultaneous-"+strconv.Itoa(n), func(b *testing.B) {
		benchLeasedBurst(b, n)
	})
}

// benchLeasedBurst runs b.N bursts of deliveries, each against a fresh root and released at once.
func benchLeasedBurst(b *testing.B, deliveries int) {
	if deliveries < 1 {
		b.Fatalf("-daemon.burst=%d: a burst needs at least one delivery", deliveries)
	}
	latencies := make([]time.Duration, 0, b.N*deliveries)
	for range b.N {
		b.StopTimer()
		lb := newLeasedBench(b, 1, deliveries)
		lb.ing.Start(b.Context(), 0, acknowledgeEveryDelivery)
		took := make([]time.Duration, deliveries)
		errs := make([]error, deliveries)
		var accepted sync.WaitGroup
		release := make(chan struct{})
		var arrival time.Time
		accepted.Add(deliveries)
		for k := range deliveries {
			go func() {
				defer accepted.Done()
				<-release
				errs[k] = lb.accept(lb.warm + k)
				took[k] = time.Since(arrival)
			}()
		}
		b.StartTimer()
		arrival = time.Now()
		close(release)
		accepted.Wait()
		b.StopTimer()

		// No Accept is running any more, so nothing sends on the ring: close it, let the workers
		// finish every queued job, and join them before reading what they committed.
		close(lb.ing.ring)
		lb.ing.Wait()
		for k, err := range errs {
			if err != nil {
				b.Fatalf("delivery %d: %v", k, err)
			}
		}
		journal := lb.requireLeased(b, len(lb.reqs))
		if got := len(journal.acks); got != len(lb.reqs) {
			b.Fatalf("acknowledged %d of %d deliveries: the B-C side did not publish every lease", got, len(lb.reqs))
		}
		latencies = append(latencies, took...)
	}
	b.ReportMetric(benchMillis(percentileDuration(latencies, 0.50)), "p50-ms")
	b.ReportMetric(benchMillis(percentileDuration(latencies, 0.99)), "p99-ms")
	b.ReportMetric(benchMillis(percentileDuration(latencies, 1)), "max-ms")
}

// acknowledgeEveryDelivery stands in for the daemon's handler (Daemon.runIngested) in the burst
// benchmark: it acknowledges every request, so dispatch goes on to commit every delivery's
// frontier record.
func acknowledgeEveryDelivery(context.Context, ipc.Request) ipc.Response {
	return ipc.Response{OK: true}
}

// benchMillis expresses d in milliseconds for b.ReportMetric.
func benchMillis(d time.Duration) float64 { return float64(d) / float64(time.Millisecond) }

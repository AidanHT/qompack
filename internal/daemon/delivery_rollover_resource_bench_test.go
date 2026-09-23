package daemon

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
)

// The SP20-D4 resource-cost measurement (V6 close-out C1.10, gate 5). It is a benchmark, run on
// purpose and never by `go test` alone, because it drives hundreds of thousands of durable deliveries:
//
//	go test ./internal/daemon -run '^$' -bench '^BenchmarkDeliveryRolloverResourceCost$' -benchtime 1x \
//	  -timeout 120m -daemon.rollover.out=<absolute path of file.json>
//
// The figures are also written to the benchmark's log, so a run whose file cannot be written still
// reports them. A relative -daemon.rollover.out is resolved against the package directory, where
// go test runs the binary, not against the directory go test was started in.
//
// With the production thresholds (a segment rotates at 65,536 entries or 64 MiB) it leases and
// acknowledges -daemon.rollover.deliveries deliveries (default 210,000: three rotations) through the
// real journal, from -daemon.rollover.workers concurrent callers, each acknowledging its previous
// delivery after leasing the next — so every rotation archives a window with deliveries still in
// flight, whose acknowledgements then settle ARCHIVED leases. It records, as figures only (no
// assertion on any of them, and no budget is changed by them):
//
//   - per-operation latency of lease and acknowledge, and the latency of each operation that carried
//     a rotation (the stall the hot path sees while a window is archived);
//   - the on-disk size and file count of the delivery state, split into the journals and the
//     generation store, at every 50,000 deliveries and at the end;
//   - the Go heap: live heap after a forced GC at every checkpoint, and the peak in-use heap sampled
//     every 50 ms, which is what bounds memory across rotations.
//
// What it DOES assert is correctness, so a figure is never reported for a broken run: every delivery
// is leased once with a dense per-session arrival, every acknowledgement is admitted, the journal
// rotated the expected number of times, and a sample of archived deliveries still resolves to its
// original identity afterwards.

var (
	rolloverResourceDeliveries = flag.Int("daemon.rollover.deliveries", 210_000,
		"deliveries BenchmarkDeliveryRolloverResourceCost leases and acknowledges")
	rolloverResourceWorkers = flag.Int("daemon.rollover.workers", 8,
		"concurrent callers in BenchmarkDeliveryRolloverResourceCost")
	rolloverResourceOut = flag.String("daemon.rollover.out", "",
		"also write BenchmarkDeliveryRolloverResourceCost's figures as JSON to this file "+
			"(relative to the package directory)")
)

// rolloverResourceCheckpoint is one snapshot of disk and memory.
type rolloverResourceCheckpoint struct {
	Deliveries       int     `json:"deliveries"`
	Segment          uint64  `json:"segment"`
	Generations      int64   `json:"generations"`
	JournalBytes     int64   `json:"journal_bytes"`
	JournalFiles     int     `json:"journal_files"`
	GenerationBytes  int64   `json:"generation_bytes"`
	GenerationFiles  int     `json:"generation_files"`
	OtherStateBytes  int64   `json:"other_state_bytes"`
	LiveHeapMiB      float64 `json:"live_heap_mib_after_gc"`
	ElapsedSeconds   float64 `json:"elapsed_seconds"`
	JournalMiB       float64 `json:"journal_mib"`
	GenerationMiB    float64 `json:"generation_mib"`
	TotalStateMiB    float64 `json:"total_state_mib"`
	PerHundredKDelMB float64 `json:"state_mib_per_100k_deliveries"`
}

// rolloverResourceRotation is one operation that carried a rotation.
type rolloverResourceRotation struct {
	FromSegment uint64  `json:"from_segment"`
	ToSegment   uint64  `json:"to_segment"`
	Operation   string  `json:"operation"`
	Seconds     float64 `json:"seconds"`
	AtDelivery  int     `json:"at_delivery"`
}

type rolloverResourceReport struct {
	Platform        string                       `json:"platform"`
	GoVersion       string                       `json:"go_version"`
	GOMAXPROCS      int                          `json:"gomaxprocs"`
	NumCPU          int                          `json:"num_cpu"`
	Deliveries      int                          `json:"deliveries"`
	Workers         int                          `json:"workers"`
	RolloverEntries int                          `json:"rollover_entries"`
	RolloverBytes   int64                        `json:"rollover_bytes"`
	Lease           map[string]float64           `json:"lease_ms"`
	Ack             map[string]float64           `json:"ack_ms"`
	LeaseInSegment0 map[string]float64           `json:"lease_ms_segment0"`
	LeaseLater      map[string]float64           `json:"lease_ms_after_first_rotation"`
	Rotations       []rolloverResourceRotation   `json:"rotations"`
	Checkpoints     []rolloverResourceCheckpoint `json:"checkpoints"`
	PeakHeapMiB     float64                      `json:"peak_heap_in_use_mib"`
	PeakSysMiB      float64                      `json:"peak_sys_mib"`
	WallSeconds     float64                      `json:"wall_seconds"`
}

func BenchmarkDeliveryRolloverResourceCost(b *testing.B) {
	if !enableDeliveryGenerations {
		b.Fatal("the measurement is of the enabled rollover, the production default")
	}
	b.ReportAllocs()
	for iter := 0; iter < b.N; iter++ {
		report := runRolloverResourceCost(b, *rolloverResourceDeliveries, *rolloverResourceWorkers)
		b.ReportMetric(report.Lease["p99"], "lease-p99-ms")
		b.ReportMetric(report.Ack["p99"], "ack-p99-ms")
		b.ReportMetric(report.PeakHeapMiB, "peak-heap-MiB")
		if n := len(report.Rotations); n > 0 {
			var worst float64
			for _, r := range report.Rotations {
				if r.Seconds > worst {
					worst = r.Seconds
				}
			}
			b.ReportMetric(worst, "worst-rotation-s")
		}
		if last := report.Checkpoints[len(report.Checkpoints)-1]; last.Deliveries > 0 {
			b.ReportMetric(last.PerHundredKDelMB, "state-MiB-per-100k")
		}
		// One line: a benchmark's log is cut to its first lines unless -v is given.
		line, err := json.Marshal(report)
		if err != nil {
			b.Fatal(err)
		}
		b.Logf("figures: %s", line)
		raw, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			b.Fatal(err)
		}
		if *rolloverResourceOut != "" {
			if err := os.WriteFile(*rolloverResourceOut, append(raw, '\n'), 0o600); err != nil {
				b.Fatal(err)
			}
		}
	}
}

func runRolloverResourceCost(b *testing.B, deliveries, workers int) rolloverResourceReport {
	b.Helper()
	root := b.TempDir()
	lock, err := acquireTestDeliveryLock(root)
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = lock.Release() }()
	j, err := lock.openDeliveryJournal()
	if err != nil {
		b.Fatal(err)
	}
	state := paths.Of(root).State
	ctx := context.Background()

	// Peak memory, sampled for the whole run.
	var peakHeap, peakSys uint64
	stopSampling := make(chan struct{})
	sampled := make(chan struct{})
	go func() {
		defer close(sampled)
		for {
			var ms runtime.MemStats
			runtime.ReadMemStats(&ms)
			if ms.HeapInuse > peakHeap {
				peakHeap = ms.HeapInuse
			}
			if ms.Sys > peakSys {
				peakSys = ms.Sys
			}
			select {
			case <-stopSampling:
				return
			case <-time.After(50 * time.Millisecond):
			}
		}
	}()

	segmentNow := func() uint64 {
		j.st.Lock()
		defer j.st.Unlock()
		return j.segment
	}

	var (
		mu          sync.Mutex
		leaseLat    []float64
		ackLat      []float64
		leaseSeg0   []float64
		leaseLater  []float64
		rotations   []rolloverResourceRotation
		checkpoints []rolloverResourceCheckpoint
		done        int
		firstErr    error
	)
	start := time.Now()
	record := func(op string, before, after uint64, d time.Duration) {
		ms := float64(d.Microseconds()) / 1000
		mu.Lock()
		defer mu.Unlock()
		switch op {
		case "lease":
			leaseLat = append(leaseLat, ms)
			if after == 0 {
				leaseSeg0 = append(leaseSeg0, ms)
			} else if before >= 1 {
				leaseLater = append(leaseLater, ms)
			}
		case "ack":
			ackLat = append(ackLat, ms)
		}
		if after != before {
			rotations = append(rotations, rolloverResourceRotation{
				FromSegment: before, ToSegment: after, Operation: op, Seconds: d.Seconds(), AtDelivery: done,
			})
		}
	}
	checkpoint := func(n int) {
		runtime.GC()
		var ms runtime.MemStats
		runtime.ReadMemStats(&ms)
		cp := rolloverResourceCheckpoint{
			Deliveries: n, Segment: segmentNow(), Generations: j.gen.generationCount(),
			LiveHeapMiB: float64(ms.HeapAlloc) / (1 << 20), ElapsedSeconds: time.Since(start).Seconds(),
		}
		_ = filepath.WalkDir(state, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			info, ierr := d.Info()
			if ierr != nil {
				return nil
			}
			rel, _ := filepath.Rel(state, p)
			rel = filepath.ToSlash(rel)
			switch {
			case strings.HasPrefix(rel, deliveryGenerationsDirName+"/"):
				cp.GenerationBytes += info.Size()
				cp.GenerationFiles++
			case strings.HasPrefix(rel, deliverySegmentsDir+"/") || strings.HasPrefix(rel, "delivery-"):
				cp.JournalBytes += info.Size()
				cp.JournalFiles++
			default:
				cp.OtherStateBytes += info.Size()
			}
			return nil
		})
		cp.JournalMiB = float64(cp.JournalBytes) / (1 << 20)
		cp.GenerationMiB = float64(cp.GenerationBytes) / (1 << 20)
		cp.TotalStateMiB = float64(cp.JournalBytes+cp.GenerationBytes+cp.OtherStateBytes) / (1 << 20)
		if n > 0 {
			cp.PerHundredKDelMB = cp.TotalStateMiB * 100_000 / float64(n)
		}
		mu.Lock()
		checkpoints = append(checkpoints, cp)
		mu.Unlock()
	}

	const checkpointEvery = 50_000
	next := make(chan int)
	var wg sync.WaitGroup
	fail := func(err error) {
		mu.Lock()
		if firstErr == nil {
			firstErr = err
		}
		mu.Unlock()
	}
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			session := core.SessionID(fmt.Sprintf("resource-session-%02d", w))
			var pending *deliveryLease
			settle := func() {
				if pending == nil {
					return
				}
				before := segmentNow()
				t0 := time.Now()
				err := j.acknowledge(ctx, pending.Delivery, pending.ObservationID, core.Hash{})
				record("ack", before, segmentNow(), time.Since(t0))
				if err != nil {
					fail(fmt.Errorf("acknowledge %s: %w", pending.Delivery, err))
				}
				pending = nil
			}
			var lastArrival uint64
			for i := range next {
				nonce := genNonce(i)
				before := segmentNow()
				t0 := time.Now()
				l, err := j.lease(ctx, nonce, session, testDeliveryRequest(nonce))
				record("lease", before, segmentNow(), time.Since(t0))
				if err != nil {
					fail(fmt.Errorf("lease %d: %w", i, err))
					continue
				}
				if l.ArrivalSeq != lastArrival+1 {
					fail(fmt.Errorf("session %s: arrival %d after %d", session, l.ArrivalSeq, lastArrival))
				}
				lastArrival = l.ArrivalSeq
				settle() // the previous delivery settles after the next one is leased
				pending = &l
			}
			settle()
		}(w)
	}
	checkpoint(0)
	for i := 0; i < deliveries; i++ {
		next <- i
		mu.Lock()
		done = i + 1
		failed := firstErr
		mu.Unlock()
		if failed != nil {
			break
		}
		if (i+1)%checkpointEvery == 0 {
			checkpoint(i + 1)
		}
	}
	close(next)
	wg.Wait()
	if firstErr != nil {
		b.Fatal(firstErr)
	}
	checkpoint(deliveries)
	close(stopSampling)
	<-sampled

	// Correctness before any figure is believed.
	wantRotations := uint64((2*deliveries - 1) / deliveryRolloverEntries) // leases and acks both fill segments
	if seg := segmentNow(); seg < uint64(deliveries/deliveryRolloverEntries) || seg > wantRotations {
		b.Fatalf("the journal is on segment %d after %d deliveries at a %d-entry threshold",
			seg, deliveries, deliveryRolloverEntries)
	}
	for _, i := range []int{0, 1, deliveries / 3, deliveries / 2, deliveries - 1} {
		nonce := genNonce(i)
		got, found, err := j.gen.resolveLease(ctx, nonce)
		if err != nil {
			b.Fatal(err)
		}
		if !found {
			j.st.Lock()
			got, found = j.leases[nonce]
			j.st.Unlock()
		}
		if !found || got.Delivery != nonce {
			b.Fatalf("delivery %d does not resolve to its original lease after the run", i)
		}
		if !j.acknowledged(nonce) {
			b.Fatalf("delivery %d is not acknowledged after the run", i)
		}
	}

	return rolloverResourceReport{
		Platform: runtime.GOOS + "/" + runtime.GOARCH, GoVersion: runtime.Version(),
		GOMAXPROCS: runtime.GOMAXPROCS(0), NumCPU: runtime.NumCPU(),
		Deliveries: deliveries, Workers: workers,
		RolloverEntries: deliveryRolloverEntries, RolloverBytes: deliveryRolloverBytes,
		Lease: rolloverPercentiles(leaseLat), Ack: rolloverPercentiles(ackLat),
		LeaseInSegment0: rolloverPercentiles(leaseSeg0), LeaseLater: rolloverPercentiles(leaseLater),
		Rotations: rotations, Checkpoints: checkpoints,
		PeakHeapMiB: float64(peakHeap) / (1 << 20), PeakSysMiB: float64(peakSys) / (1 << 20),
		WallSeconds: time.Since(start).Seconds(),
	}
}

// rolloverPercentiles summarises latencies in milliseconds.
func rolloverPercentiles(ms []float64) map[string]float64 {
	out := map[string]float64{"n": float64(len(ms))}
	if len(ms) == 0 {
		return out
	}
	s := append([]float64(nil), ms...)
	sort.Float64s(s)
	at := func(q float64) float64 {
		i := int(q * float64(len(s)-1))
		return s[i]
	}
	out["p50"], out["p90"], out["p99"], out["p999"], out["max"] = at(0.50), at(0.90), at(0.99), at(0.999), s[len(s)-1]
	return out
}

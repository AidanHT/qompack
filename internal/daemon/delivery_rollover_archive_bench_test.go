package daemon

import (
	"context"
	"flag"
	"fmt"
	"sort"
	"testing"
	"time"

	"github.com/qompack/qompack/internal/core"
)

var rolloverArchiveWindows = flag.Int("daemon.rollover.archive.windows", 5,
	"full windows BenchmarkDeliveryRolloverArchiveWindows archives in turn")

// BenchmarkDeliveryRolloverArchiveWindows measures the work a rotation does under its barrier:
// archiveWindow of successive full 65,536-lease windows (8 sessions, dense arrivals continuing from
// window to window, every lease acknowledged but the last two of each session) into one generation
// store, so each window lands in a larger tree. It logs each window's wall time and the generations
// committed so far. It is run on purpose, never by go test alone:
//
//	go test ./internal/daemon -run '^$' -bench '^BenchmarkDeliveryRolloverArchiveWindows$' -benchtime 1x
//
// Figures only; nothing is asserted on them.
func BenchmarkDeliveryRolloverArchiveWindows(b *testing.B) {
	const window, sessions = deliveryLeaseMaxEntries, 8
	for iter := 0; iter < b.N; iter++ {
		g, err := openDeliveryGenerations(b.TempDir())
		if err != nil {
			b.Fatal(err)
		}
		for w := 0; w < *rolloverArchiveWindows; w++ {
			leases, acks := benchArchiveWindow(b, w, window, sessions)
			start := time.Now()
			if err := g.archiveWindow(context.Background(), leases, acks, nil); err != nil {
				b.Fatal(err)
			}
			b.Logf("window %d: %v, generations=%d", w, time.Since(start), g.generationCount())
		}
		if err := g.close(); err != nil {
			b.Fatal(err)
		}
	}
}

// benchArchiveWindow is window w of the benchmark: its leases in (session, arrival) order, as the
// rotation passes them, and the acknowledgements of all but each session's last two.
func benchArchiveWindow(tb testing.TB, w, n, sessions int) ([]deliveryLease, []deliveryAck) {
	tb.Helper()
	per := n / sessions
	leases := make([]deliveryLease, 0, n)
	acks := make([]deliveryAck, 0, n)
	for s := 0; s < sessions; s++ {
		session := core.SessionID(fmt.Sprintf("bench-archive-%d", s))
		for a := 1; a <= per; a++ {
			arrival := uint64(w*per + a)
			obs, err := core.NewObservationID(session, arrival)
			if err != nil {
				tb.Fatal(err)
			}
			nonce := genNonce(w*n + s*per + a)
			l := deliveryLease{
				Version: core.EvidenceVersion, Delivery: nonce, Session: session,
				RequestHash: testDeliveryRequest(nonce), ArrivalSeq: arrival, ObservationID: obs,
			}
			leases = append(leases, l)
			if a <= per-2 {
				acks = append(acks, deliveryAck{Version: core.EvidenceVersion, Delivery: nonce, ObservationID: obs})
			}
		}
	}
	return leases, acks
}

// BenchmarkDeliveryRolloverDispatchGate measures what rollover adds to the same-session ordering gate
// (predecessorsAcknowledged, delivery_order.go), which every leased dispatch and drain look-ahead calls
// with the owner mutex and the journal's state mutex held (review finding 5). Before the first rotation
// the generation store is empty and its two lookups (the session's last arrival and its settled
// frontier) return at once; after it they are radix reads, some of them positioned reads of pack
// records. It archives -daemon.rollover.archive.windows full windows into a live journal's generation
// store, then times the gate for sessions whose history is archived, and logs the distribution. The
// active window is left empty, so what is timed is the store's part; the scan of the active window the
// gate also does is the same with rollover on or off. Figures only.
//
//	go test ./internal/daemon -run '^$' -bench '^BenchmarkDeliveryRolloverDispatchGate$' -benchtime 1x
func BenchmarkDeliveryRolloverDispatchGate(b *testing.B) {
	const window, sessions, calls = deliveryLeaseMaxEntries, 8, 20_000
	prevEnable := enableDeliveryGenerations
	enableDeliveryGenerations = true
	b.Cleanup(func() { enableDeliveryGenerations = prevEnable })
	for iter := 0; iter < b.N; iter++ {
		root := b.TempDir()
		lock, err := acquireTestDeliveryLock(root)
		if err != nil {
			b.Fatal(err)
		}
		j, err := lock.openDeliveryJournal()
		if err != nil {
			b.Fatal(err)
		}
		// Timed in batches of 100 calls: a single call is below the resolution of the Windows clock.
		gateLatencies := func(label string) {
			const batch = 100
			means := make([]time.Duration, 0, calls/batch)
			per := uint64(*rolloverArchiveWindows * window / sessions)
			total := time.Duration(0)
			for i := 0; i < calls; i += batch {
				start := time.Now()
				for k := i; k < i+batch; k++ {
					session := core.SessionID(fmt.Sprintf("bench-archive-%d", k%sessions))
					arrival := 2 + uint64(k*7919)%per // spread over the archived arrivals
					_ = j.predecessorsAcknowledged(session, arrival)
				}
				d := time.Since(start)
				total += d
				means = append(means, d/batch)
			}
			sort.Slice(means, func(a, c int) bool { return means[a] < means[c] })
			q := func(p float64) time.Duration { return means[int(p*float64(len(means)-1))] }
			b.Logf("%s: %d calls, mean %v per call; per-call mean of each %d-call batch: p50 %v, p90 %v, p99 %v, max %v",
				label, calls, total/calls, batch, q(0.5), q(0.9), q(0.99), means[len(means)-1])
		}
		gateLatencies("gate, empty generation store")
		for w := 0; w < *rolloverArchiveWindows; w++ {
			leases, acks := benchArchiveWindow(b, w, window, sessions)
			if err := j.gen.archiveWindow(context.Background(), leases, acks, nil); err != nil {
				b.Fatal(err)
			}
		}
		gateLatencies(fmt.Sprintf("gate, %d archived windows, first calls", *rolloverArchiveWindows))
		gateLatencies(fmt.Sprintf("gate, %d archived windows, warm", *rolloverArchiveWindows))
		if err := lock.Release(); err != nil {
			b.Fatal(err)
		}
	}
}

package daemon

import (
	"context"
	"flag"
	"fmt"
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

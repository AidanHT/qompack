package ipc

import "time"

// Probe reports whether a live peer is listening at a: it dials with timeout and immediately
// closes the connection without writing anything (00-ARCHITECTURE.md §2.4: "dial the recorded
// addr ... success -> alive").
//
// This is deliberately weaker than a round trip through Client.Send: a true result means only
// "something accepted the connection", never "some particular op was handled successfully". That
// is exactly the primitive daemon.AcquireLock's staleness protocol and daemon.EnsureRunning need —
// neither cares what a live daemon would say to any specific request, only whether one is there at
// all — and it is the only liveness signal that degrades correctly if a future op-routing table
// ever answers a probe op with a refusal: a refusal from Client.Send is indistinguishable from
// "nobody is listening", but a successful Probe dial never is.
func Probe(a Addr, timeout time.Duration) bool {
	conn, err := dial(a, timeout)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

package e2e

import (
	"fmt"
	"io"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestSyncBufferConcurrentWriterReader reproduces, in isolation, the access pattern that raced in
// mcpE2EChild: os/exec drains a child's stderr into cmd.Stderr from a goroutine it owns (an io.Copy
// that keeps writing for the whole life of the process) while the test reads that same buffer on
// every send/await/finish. The concrete failure captured at
// plans/sdd/V6-remediation/linux-product-child-race-artifacts/race.8310 is a write in
// bytes.(*Buffer).grow racing a read in bytes.(*Buffer).String.
//
// This drives syncBuffer through exactly that shape — one writer copying bytes in via io.Copy (the
// os/exec side), several readers calling String concurrently (the assertion side) — so that `go
// test -race` on this one function proves the guarded buffer serializes them. It is deliberately
// self-contained: it builds no binary and starts no child, so it can be the single focused helper
// test run under the race detector without invoking the full end-to-end path or Docker.
func TestSyncBufferConcurrentWriterReader(t *testing.T) {
	t.Parallel()

	const (
		writes  = 512
		readers = 8
	)

	var b syncBuffer

	// The writer mirrors os/exec's stderr copy: io.Copy from a producer straight into the buffer as
	// an io.Writer, exercising the same Write path (grow included) the real child drives.
	pr, pw := io.Pipe()
	copied := make(chan struct{})
	go func() {
		defer close(copied)
		_, _ = io.Copy(&b, pr)
	}()
	go func() {
		defer func() { _ = pw.Close() }()
		for i := 0; i < writes; i++ {
			// Distinct, growing lines so the buffer must reallocate its backing array while the
			// readers below are reading — the write side of the original race.
			fmt.Fprintf(pw, "mcp stderr line %06d: a diagnostic the child printed\n", i)
		}
	}()

	// The readers mirror the assertions in send/await/finish: String() called repeatedly, live,
	// while the copy is still running. A plain bytes.Buffer here trips the detector; syncBuffer must
	// not.
	var wg sync.WaitGroup
	stop := make(chan struct{})
	for r := 0; r < readers; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					_ = b.String()
				}
			}
		}()
	}

	<-copied
	close(stop)
	wg.Wait()

	// No bytes were dropped or corrupted: the guard only serializes access, it does not lose data.
	got := b.String()
	require.Contains(t, got, "mcp stderr line 000000:")
	require.Contains(t, got, fmt.Sprintf("mcp stderr line %06d:", writes-1))
	require.Len(t, got, writes*len("mcp stderr line 000000: a diagnostic the child printed\n"))
}

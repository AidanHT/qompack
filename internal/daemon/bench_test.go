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

package negknow

import (
	"context"
	"strconv"
	"testing"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/store"
)

// BenchmarkIngestMCPRealStore prices one record_eliminated ingest over the real store the daemon
// hands the ledger: evidence minted through PutBytes, then — since w6-ckptsync 2df3776 — the
// evidence's publication pass and the log line's sync (durable.go), then the answer. Every
// iteration records a distinct elimination, so none is a dedup hit. It is read as a ratio against
// its own base, never as a budget: record_eliminated is an explicit agent call, not a hot path.
func BenchmarkIngestMCPRealStore(b *testing.B) {
	root, cfg := benchProject(b)
	st, err := store.Open(root, cfg, store.Deps{Log: logging.Nop(), Clock: core.SystemClock()})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = st.Close() })
	deps := benchDeps(nil, nil)
	deps.Store = st
	l := benchOpen(b, root, cfg, deps)
	ctx := context.Background()

	b.ReportAllocs()
	b.ResetTimer()
	for i := range b.N {
		n := strconv.Itoa(i)
		if _, _, err := l.IngestMCP(ctx, MCPArgs{
			Target: "src/bench/f" + n + ".ts:run", Approach: "widen pool timeout " + n,
			Reason: "measured: the pool is not the bottleneck, iteration " + n, Scope: "project",
		}); err != nil {
			b.Fatal(err)
		}
	}
}

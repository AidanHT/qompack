package store

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/redact"
)

// benchStore opens a store under b's temp directory, isolated from the developer's home.
func benchStore(b *testing.B, opts ...storeOpt) *FSStore {
	b.Helper()
	base := b.TempDir()
	root := filepath.Join(base, "project")
	home := filepath.Join(base, "home")
	for _, d := range []string{root, home} {
		if err := os.MkdirAll(paths.Long(d), 0o700); err != nil {
			b.Fatal(err)
		}
	}
	b.Setenv("QOMPACK_HOME", filepath.Join(home, ".qompack"))
	b.Setenv("HOME", home)
	b.Setenv("USERPROFILE", home)

	cfg := config.Defaults()
	// Chunker and Canon are left nil so defaultDeps installs the production chunk.New and
	// canon.Default. V2-SP06-25's budgets are stated against the real pipeline, so measuring an
	// injected chunker and a no-op canonicalizer would report a number for code that never ships.
	deps := Deps{
		Log:   logging.Nop(),
		Clock: newFakeClock(),
	}
	for _, o := range opts {
		o(&cfg, &deps)
	}
	s, err := openFS(root, cfg, deps)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = s.Close() })
	return s
}

// withNopRedactor injects an inert redactor, so a benchmark can separate what the STORE costs from
// what redaction costs. Production always uses the real redactor (Deps.Redact nil defaults to it),
// so this is a measurement aid, never a configuration.
func withNopRedactor() storeOpt {
	return func(_ *config.Config, d *Deps) { d.Redact = redact.Nop() }
}

// BenchmarkPutBytes_100KB_Warm_NoRedact isolates the store's own warm-path cost from redaction's.
func BenchmarkPutBytes_100KB_Warm_NoRedact(b *testing.B) {
	ctx := context.Background()
	s := benchStore(b, withNopRedactor())
	payload := benchPayload(100 << 10)

	if _, err := s.PutBytes(ctx, payload, PutOptions{Tool: "FileRead", Path: "src/bench.ts"}); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.SetBytes(int64(len(payload)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := s.PutBytes(ctx, payload, PutOptions{Tool: "FileRead", Path: "src/bench.ts"}); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkPutBytes_100KB_Cold_NoRedact isolates the store's own cold-path cost from redaction's.
func BenchmarkPutBytes_100KB_Cold_NoRedact(b *testing.B) {
	ctx := context.Background()
	payload := benchPayload(100 << 10)

	b.ReportAllocs()
	b.SetBytes(int64(len(payload)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		s := benchStore(b, withNopRedactor())
		p := append([]byte(fmt.Sprintf("iteration %d\n", i)), payload...)
		b.StartTimer()
		if _, err := s.PutBytes(ctx, p, PutOptions{Tool: "FileRead", Path: "src/bench.ts"}); err != nil {
			b.Fatal(err)
		}
	}
}

// benchPayload is a deterministic 100 KB payload for the Put benchmarks.
func benchPayload(n int) []byte { return bigPayload(n) }

// BenchmarkPutBytes_100KB_Cold measures a first ingest of 100 KB into an empty store: budget
// ≤ 3 ms, sized against B-C (l0_process p99 < 50 ms) with headroom for canon, chunking and the DAG.
func BenchmarkPutBytes_100KB_Cold(b *testing.B) {
	ctx := context.Background()
	payload := benchPayload(100 << 10)

	b.ReportAllocs()
	b.SetBytes(int64(len(payload)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		s := benchStore(b)
		// Vary the content per iteration so every run is genuinely cold.
		p := append([]byte(fmt.Sprintf("iteration %d\n", i)), payload...)
		b.StartTimer()

		if _, err := s.PutBytes(ctx, p, PutOptions{Tool: "FileRead", Path: "src/bench.ts"}); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkPutBytes_100KB_Warm measures re-ingesting identical content: the four-reads-of-one-file
// case of §8.2. Budget ≤ 400 µs — it must be a pure index lookup plus a root-level dedup hit.
func BenchmarkPutBytes_100KB_Warm(b *testing.B) {
	ctx := context.Background()
	s := benchStore(b)
	payload := benchPayload(100 << 10)

	if _, err := s.PutBytes(ctx, payload, PutOptions{Tool: "FileRead", Path: "src/bench.ts"}); err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()
	b.SetBytes(int64(len(payload)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := s.PutBytes(ctx, payload, PutOptions{Tool: "FileRead", Path: "src/bench.ts"}); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkPutBytes_100KB_Warm_KeepRaw measures the root-level dedup hit of a KeepRaw root, whose
// label dedupFidelity derives from this put's own bytes (SP20-D3): verbatim compares the canonical
// bytes with the input, delta re-derives the record's address and replays the deltas, and full
// re-derives the retained original's address. BenchmarkPutBytes_100KB_Warm cannot see that cost:
// without KeepRaw a root carries no recovery record and the label is canonical at once. The same
// ≤ 400 µs warm budget applies.
func BenchmarkPutBytes_100KB_Warm_KeepRaw(b *testing.B) {
	body := benchPayload(100 << 10)
	cases := []struct {
		name    string
		opts    []storeOpt
		payload []byte
		want    Fidelity
	}{
		{"verbatim", nil, body, FidelityExact},
		{"delta", nil, append([]byte("built at 2026-09-09T10:00:00Z\n"), body...), FidelityExact},
		{
			"full",
			[]storeOpt{withCanon(canonLossyDeltas())},
			append([]byte(timestampPrefix+"09:11:04\n"), body...), FidelityFull,
		},
	}
	for _, c := range cases {
		b.Run(c.name, func(b *testing.B) {
			ctx := context.Background()
			s := benchStore(b, c.opts...)
			o := PutOptions{Tool: "Bash", Path: "src/bench.log", KeepRaw: true}
			first, err := s.PutBytes(ctx, c.payload, o)
			if err != nil {
				b.Fatal(err)
			}
			s.mu.RLock()
			e := *s.rootIndex[first.Root.Hash]
			s.mu.RUnlock()
			// Fixture sanity: each case must actually store the recovery shape it is named for.
			if first.Fidelity != c.want || (c.name == "delta") == e.Deltas.IsZero() ||
				(c.name == "full") == e.Orig.IsZero() {
				b.Fatalf("fixture stored fidelity %s, deltas %v, orig %v", first.Fidelity,
					!e.Deltas.IsZero(), !e.Orig.IsZero())
			}

			b.ReportAllocs()
			b.SetBytes(int64(len(c.payload)))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := s.PutBytes(ctx, c.payload, o); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkPutObject_NovelChunk measures the write PutBytes pays for each novel chunk of a cold put
// (SP06-D2): the presence stat, the zstd encode, the fanout-leaf create, the exclusive staging
// create and write, and the publishing rename. It leaves out what surrounds those in PutBytes —
// redaction, canonicalization, chunking, the pending-write marker with its fsync, and the index
// append — so the per-chunk filesystem cost can be compared on its own. Every iteration writes a
// 4 KiB chunk no earlier iteration wrote into one store, so the fanout directories fill as a real
// store's do; computing the chunk's address (about 4 KiB of SHA-256) is inside the timed loop.
func BenchmarkPutObject_NovelChunk(b *testing.B) {
	s := benchStore(b)
	plain := benchPayload(4 << 10)

	b.ReportAllocs()
	b.SetBytes(int64(len(plain)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		binary.LittleEndian.PutUint64(plain, uint64(i))
		h := core.HashBytes(core.DomainChunk, plain)
		if _, novel, err := s.putObject(h, plain); err != nil || !novel {
			b.Fatalf("iteration %d: novel %v, err %v", i, novel, err)
		}
	}
}

// BenchmarkGetChunk measures a warm single-chunk read: budget ≤ 60 µs, sized so `expand` fits
// inside B-F (MCP tool call p95 < 250 ms).
func BenchmarkGetChunk(b *testing.B) {
	ctx := context.Background()
	s := benchStore(b)

	res, err := s.PutBytes(ctx, benchPayload(100<<10), PutOptions{Path: "src/bench.ts"})
	if err != nil {
		b.Fatal(err)
	}
	h := res.Root.Chunks[len(res.Root.Chunks)/2].Hash

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := s.GetChunk(ctx, h); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkOpenSpan_4KB_of_4MB measures the minimal-sufficient-span read §8.7 makes the default:
// 4 KB out of a 4 MB root must decompress only the chunks the span touches. Budget ≤ 150 µs.
func BenchmarkOpenSpan_4KB_of_4MB(b *testing.B) {
	ctx := context.Background()
	s := benchStore(b)

	payload := benchPayload(4 << 20)
	res, err := s.PutBytes(ctx, payload, PutOptions{Path: "src/big.ts"})
	if err != nil {
		b.Fatal(err)
	}
	mid := int64(len(payload) / 2)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rc, err := s.OpenSpan(ctx, res.Root.Hash, mid, 4096)
		if err != nil {
			b.Fatal(err)
		}
		if _, err := io.Copy(io.Discard, rc); err != nil {
			b.Fatal(err)
		}
		_ = rc.Close()
	}
}

// BenchmarkOpenStore_50kRoots measures daemon start over a large index: budget ≤ 400 ms. It is off
// the hot path, but it is what a session pays before its first hook can be served.
func BenchmarkOpenStore_50kRoots(b *testing.B) {
	const roots = 50_000

	base := b.TempDir()
	root := filepath.Join(base, "project")
	home := filepath.Join(base, "home")
	for _, d := range []string{root, home} {
		if err := os.MkdirAll(paths.Long(d), 0o700); err != nil {
			b.Fatal(err)
		}
	}
	b.Setenv("QOMPACK_HOME", filepath.Join(home, ".qompack"))
	b.Setenv("HOME", home)
	b.Setenv("USERPROFILE", home)
	if err := paths.EnsureLayout(paths.Of(root)); err != nil {
		b.Fatal(err)
	}

	// Write the index directly: the point is to measure the LOADER, not 50k ingests.
	w, err := openAppendFile(filepath.Join(paths.Of(root).Index, rootsFile))
	if err != nil {
		b.Fatal(err)
	}
	for i := 0; i < roots; i++ {
		seed := []byte(fmt.Sprintf("root-%d", i))
		rl := rootEntry{
			Root: Root{
				Hash: core.HashBytes(core.DomainRoot, seed),
				Chunks: []ChunkRef{
					{Hash: core.HashBytes(core.DomainChunk, append(seed, 'a')), Len: 4096},
					{Hash: core.HashBytes(core.DomainChunk, append(seed, 'b')), Len: 3771},
				},
				CanonBytes: 7867, RawBytes: 8000, Tokens: 2000,
			},
			TS: core.UnixMilli(1734128400000 + int64(i)), Tool: "FileRead",
			Path: fmt.Sprintf("src/f%d.ts", i%1000),
		}
		if err := w.write(marshalRootLine(rl)); err != nil {
			b.Fatal(err)
		}
	}
	if err := w.close(); err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s, err := openFS(root, config.Defaults(), Deps{Log: logging.Nop(), Clock: newFakeClock()})
		if err != nil {
			b.Fatal(err)
		}
		b.StopTimer()
		_ = s.Close()
		b.StartTimer()
	}
}

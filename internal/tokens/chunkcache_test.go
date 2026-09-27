package tokens_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/obs"
	"github.com/qompack/qompack/internal/tokens"
)

// closer is the Flush/Close seam the exact estimator adds beyond the frozen §5.20 Estimator
// interface, so the store can persist the chunk-token cache at SessionEnd.
type closer interface {
	Flush() error
	Close() error
}

// chunkSink asserts e implements the seam store.Put feeds measured chunks through, and returns it.
// The checked two-value form is used deliberately: a bare type assertion would panic on a future
// estimator that stopped implementing ChunkSink, which reads as a crash rather than as the
// contract violation it is.
func chunkSink(tb testing.TB, e tokens.Estimator) tokens.ChunkSink {
	tb.Helper()
	s, ok := e.(tokens.ChunkSink)
	require.True(tb, ok, "the exact estimator must implement tokens.ChunkSink")
	return s
}

// persister asserts e implements the Flush/Close durability seam, and returns it.
func persister(tb testing.TB, e tokens.Estimator) closer {
	tb.Helper()
	c, ok := e.(closer)
	require.True(tb, ok, "the exact estimator must implement Flush and Close")
	return c
}

// TestChunkCache_Persists asserts measured chunks survive a Close/reopen cycle, and pins the
// on-disk framing: a "QPKT" header followed by fixed-width records.
func TestChunkCache_Persists(t *testing.T) {
	cachePath := filepath.Join(t.TempDir(), "chunktokens.bin")

	est := tokens.NewExact(config.Defaults(), "", cachePath)
	sink := chunkSink(t, est)

	type sample struct {
		ref  core.ChunkRef
		want core.Tokens
	}
	var samples []sample
	for i := 0; i < 100; i++ {
		body := bytes.Repeat([]byte("persisted chunk "), 3+i%9)
		h := core.HashBytes("tokens.persist", append([]byte{byte(i)}, body...))
		samples = append(samples, sample{
			ref:  core.ChunkRef{Hash: h, Len: len(body)},
			want: sink.NoteChunk(h, tokens.ClassProse, body),
		})
	}
	require.NoError(t, persister(t, est).Close())

	// The file framing is a wire format: header magic, version, then fixed-width records.
	raw, err := os.ReadFile(cachePath)
	require.NoError(t, err)
	require.True(t, bytes.HasPrefix(raw, tokens.ChunkCacheHeaderMagic()), "header must start with QPKT")
	require.Equal(t, uint16(1), binary.LittleEndian.Uint16(raw[4:6]), "header version")
	body := raw[tokens.ChunkCacheHeaderSize():]
	require.Zero(t, len(body)%tokens.ChunkCacheRecordSize(), "the record region must be a whole number of records")
	require.Equal(t, 100, len(body)/tokens.ChunkCacheRecordSize())

	// A fresh estimator over the same file reloads every measurement: no misses, same numbers.
	reg := obs.New(core.SystemClock())
	reopened := tokens.NewExactWithObs(config.Defaults(), "", cachePath, nil, reg)
	for _, s := range samples {
		require.Equal(t, s.want, reopened.EstimateRoot(context.Background(), []core.ChunkRef{s.ref}, tokens.ClassProse))
	}
	require.Zero(t, reg.Counter("tokens.chunk_miss").Value(), "every reloaded chunk must be a cache hit")
}

// TestChunkCache_CorruptFile asserts a truncated cache loads its intact prefix instead of failing:
// the cache is an optimisation, and losing it must never be the reason a session cannot start.
func TestChunkCache_CorruptFile(t *testing.T) {
	cachePath := filepath.Join(t.TempDir(), "chunktokens.bin")

	est := tokens.NewExact(config.Defaults(), "", cachePath)
	sink := chunkSink(t, est)

	body := bytes.Repeat([]byte("intact chunk "), 6)
	kept := core.HashBytes("tokens.corrupt", body)
	want := sink.NoteChunk(kept, tokens.ClassProse, body)
	for i := 0; i < 4; i++ {
		other := bytes.Repeat([]byte("later chunk "), 4+i)
		sink.NoteChunk(core.HashBytes("tokens.corrupt", append([]byte{byte(i)}, other...)), tokens.ClassProse, other)
	}
	require.NoError(t, persister(t, est).Close())

	// Truncate mid-record: keep the header, the first whole record, and a partial second one.
	raw, err := os.ReadFile(cachePath)
	require.NoError(t, err)
	cut := tokens.ChunkCacheHeaderSize() + tokens.ChunkCacheRecordSize() + 7
	require.Less(t, cut, len(raw), "fixture sanity: the cache must have more than one record")
	require.NoError(t, os.WriteFile(cachePath, raw[:cut], 0o644))

	reg := obs.New(core.SystemClock())
	reopened := tokens.NewExactWithObs(config.Defaults(), "", cachePath, nil, reg)
	require.NotNil(t, reopened, "a truncated cache must not prevent construction")

	got := reopened.EstimateRoot(context.Background(), []core.ChunkRef{{Hash: kept, Len: len(body)}}, tokens.ClassProse)
	require.Equal(t, want, got, "the intact prefix of a truncated cache must still load")
}

// TestChunkCache_EmptyPathIsInMemoryOnly asserts an estimator built without a cache path is still
// fully functional — it just has nothing to persist.
func TestChunkCache_EmptyPathIsInMemoryOnly(t *testing.T) {
	est := tokens.NewExact(config.Defaults(), "", "")
	sink := chunkSink(t, est)

	body := []byte("in-memory only chunk body, never written anywhere")
	h := core.HashBytes("tokens.inmem", body)
	want := sink.NoteChunk(h, tokens.ClassProse, body)

	require.Equal(t, want, est.EstimateRoot(context.Background(), []core.ChunkRef{{Hash: h, Len: len(body)}}, tokens.ClassProse))
	require.NoError(t, persister(t, est).Flush())
	require.NoError(t, persister(t, est).Close())
}

// TestChunkCache_ConcurrentFlushesNeverOverlapOnDisk runs Flush from several goroutines at once, as
// concurrent session ends do (each one's store.Flush reaches the estimator). Each flush takes its
// pending records under the cache lock but did its file I/O after releasing it, so two flushes could
// overlap on disk: an append's read-write handle, which on Windows carries no FILE_SHARE_DELETE,
// made a concurrent compaction's paths.WriteAtomic replace fail, and two appends that measured the
// same record count wrote their records over each other's. A cap of a few entries makes nearly
// every other flush a compaction, so the overlap is exercised on every run.
func TestChunkCache_ConcurrentFlushesNeverOverlapOnDisk(t *testing.T) {
	defer tokens.SetChunkCacheMaxEntries(concurrentFlushCap)()
	cachePath := filepath.Join(t.TempDir(), "chunktokens.bin")
	est := tokens.NewExact(config.Defaults(), "", cachePath)
	sink := chunkSink(t, est)
	flusher := persister(t, est)

	errs := make(chan error, concurrentFlushers*concurrentFlushRounds)
	var wg sync.WaitGroup
	for g := 0; g < concurrentFlushers; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for r := 0; r < concurrentFlushRounds; r++ {
				body := bytes.Repeat([]byte("concurrent flush chunk "), 2+(g+r)%5)
				h := core.HashBytes("tokens.concurrent", append([]byte{byte(g), byte(r)}, body...))
				sink.NoteChunk(h, tokens.ClassProse, body)
				errs <- flusher.Flush()
			}
		}(g)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err, "a flush must never fail because another flush was writing the same file")
	}
	require.NoError(t, flusher.Close())

	raw, err := os.ReadFile(cachePath)
	require.NoError(t, err)
	body := raw[tokens.ChunkCacheHeaderSize():]
	require.Zero(t, len(body)%tokens.ChunkCacheRecordSize(), "the record region must be whole records")
	records := len(body) / tokens.ChunkCacheRecordSize()
	require.Equal(t, uint64(records), binary.LittleEndian.Uint64(raw[8:16]),
		"the header's record count must match the records on disk")
	seen := map[string]bool{}
	for i := 0; i < records; i++ {
		rec := body[i*tokens.ChunkCacheRecordSize() : (i+1)*tokens.ChunkCacheRecordSize()]
		key := string(rec[:len(core.Hash{})])
		require.False(t, seen[key], "record %d repeats a hash: two flushes wrote over each other", i)
		seen[key] = true
	}
}

// concurrentFlushCap, concurrentFlushers and concurrentFlushRounds size that test: a cap of four
// entries makes a file past chunkCacheCompactFactor (2) times the cap after a few flushes, so most
// rounds compact; eight goroutines of forty rounds give 320 flushes that overlap on any scheduler.
const (
	concurrentFlushCap    = 4
	concurrentFlushers    = 8
	concurrentFlushRounds = 40
)

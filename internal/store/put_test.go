package store

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/canon"
	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/sketch"
)

// awsExampleKey is AWS's own long-standing documentation example access key ID. It is a redaction
// FIXTURE, never a credential.
//
//nolint:gosec // G101: this IS the redaction fixture; see comment above.
const awsExampleKey = "@@SEC_AWS_AKID@@"

// withChunkSize overrides the test chunker's boundary, so a test that must move a lot of bytes can
// do it in a few large chunks instead of thousands of small ones.
func withChunkSize(n int) storeOpt {
	return func(_ *config.Config, d *Deps) { d.Chunker = fixedChunker{size: n} }
}

// TestPutBytes_GlobalDedup asserts an edited file costs only its edit.
//
// The bound is scoped to v1..v3 deliberately. Those are small edits — 2 and 18 lines — which is
// the case Qompack.md §6.1's "four reads of a 2,000-line file collapse to one chunk set plus three
// near-empty reference lists" is actually about. v4 is a 240-line rewrite that also grows the file
// by 19 %, so it legitimately writes MORE chunks than v1 did; asserting otherwise would be
// asserting that content-defined chunking dedups content that genuinely is not there.
// TestPutBytes_LargeRewriteStillSharesChunks covers v4 on its own terms.
func TestPutBytes_GlobalDedup(t *testing.T) {
	tp := newTestStore(t)
	ctx := context.Background()

	var roots []core.Hash
	var novel []int
	var afterV1 int
	for i, name := range []string{"fileread-auth-v1.txt", "fileread-auth-v2.txt", "fileread-auth-v3.txt"} {
		res, err := tp.Store.PutBytes(ctx, fixture(t, name), PutOptions{Tool: "FileRead", Path: "src/auth.ts"})
		require.NoError(t, err)
		roots = append(roots, res.Root.Hash)
		novel = append(novel, res.Novel)
		if i == 0 {
			afterV1 = len(tp.objectPaths(t))
		}
	}

	require.Len(t, uniqueHashes(roots), 3, "three different versions must produce three different roots")
	require.Positive(t, novel[0], "the first read of new content must write novel chunks")
	for i := 1; i < len(novel); i++ {
		require.Less(t, novel[i], novel[0]/4,
			"a small edit must cost far less than a first read; version %d wrote %d novel chunks vs %d",
			i+1, novel[i], novel[0])
	}

	total := len(tp.objectPaths(t))
	require.Less(t, total, int(1.6*float64(afterV1)),
		"three versions must cost well under 1.6x one version's objects; got %d vs %d for v1 alone", total, afterV1)
}

// TestPutBytes_LargeRewriteStillSharesChunks asserts that even a 240-line rewrite reuses a
// substantial part of the prior version's chunk set, rather than degenerating into a full second
// copy the way a fixed-size splitter would.
func TestPutBytes_LargeRewriteStillSharesChunks(t *testing.T) {
	tp := newTestStore(t)
	ctx := context.Background()

	v3, err := tp.Store.PutBytes(ctx, fixture(t, "fileread-auth-v3.txt"), PutOptions{Tool: "FileRead", Path: "src/auth.ts"})
	require.NoError(t, err)
	v4, err := tp.Store.PutBytes(ctx, fixture(t, "fileread-auth-v4.txt"), PutOptions{Tool: "FileRead", Path: "src/auth.ts"})
	require.NoError(t, err)

	require.NotEqual(t, v3.Root.Hash, v4.Root.Hash)
	require.Positive(t, v4.Reused, "a large rewrite must still reuse the chunks it did not touch")
	require.Less(t, v4.Novel, len(v4.Root.Chunks),
		"a rewrite that reuses nothing at all would mean boundary stability is broken")
}

// uniqueHashes reduces hs to its distinct members.
func uniqueHashes(hs []core.Hash) []core.Hash {
	seen := map[core.Hash]struct{}{}
	var out []core.Hash
	for _, h := range hs {
		if _, ok := seen[h]; !ok {
			seen[h] = struct{}{}
			out = append(out, h)
		}
	}
	return out
}

// TestPutBytes_IdenticalRootIsFree asserts a second put of byte-identical content writes no object
// and appends no roots.jsonl line: it is a pure reference.
func TestPutBytes_IdenticalRootIsFree(t *testing.T) {
	tp := newTestStore(t)
	ctx := context.Background()
	payload := fixture(t, "fileread-auth-v1.txt")

	first, err := tp.Store.PutBytes(ctx, payload, PutOptions{Tool: "FileRead", Path: "src/auth.ts"})
	require.NoError(t, err)
	require.Positive(t, first.Novel)
	linesAfterFirst := len(tp.indexLines(t, rootsFile))

	second, err := tp.Store.PutBytes(ctx, payload, PutOptions{Tool: "Grep", Path: "src/other.ts"})
	require.NoError(t, err)
	require.Equal(t, first.Root.Hash, second.Root.Hash, "identical bytes must dedup regardless of tool/path")
	require.Zero(t, second.Novel)
	require.Equal(t, len(second.Root.Chunks), second.Reused)
	require.Equal(t, linesAfterFirst, len(tp.indexLines(t, rootsFile)),
		"an exact-duplicate put must not append a second roots.jsonl line")
}

// TestPutBytes_DedupHitReportsThisPutsRawBytes is the regression guard for the subtlest accounting
// bug in this package: on a root-level dedup hit, PutResult must report THIS put's raw size, not
// the size of the put that first stored the root. Reporting the stored root's size would make
// Stats.RawBytes — and therefore DedupRatio, the Phase 1 exit criterion — silently wrong.
func TestPutBytes_DedupHitReportsThisPutsRawBytes(t *testing.T) {
	tp := newTestStore(t, withCanon(canonStripTimestamp()))
	ctx := context.Background()

	// Two payloads that differ ONLY in a volatile line the canonicalizer strips, so both
	// canonicalize to one root while having genuinely different raw sizes.
	short := []byte(timestampPrefix + "09:11:04\nshared body line\n")
	long := []byte(timestampPrefix + "09:11:04.000000123 +00:00 [pid 4711]\nshared body line\n")
	require.NotEqual(t, len(short), len(long), "fixture sanity: the raw sizes must differ")

	a, err := tp.Store.PutBytes(ctx, short, PutOptions{Path: "src/log.txt"})
	require.NoError(t, err)
	b, err := tp.Store.PutBytes(ctx, long, PutOptions{Path: "src/log.txt"})
	require.NoError(t, err)

	require.Equal(t, a.Root.Hash, b.Root.Hash, "both must canonicalize to one root")
	require.Zero(t, b.Novel, "the second put must be a pure reference")
	require.Equal(t, int64(len(short)), a.Root.RawBytes)
	require.Equal(t, int64(len(long)), b.Root.RawBytes, "a dedup hit must report ITS OWN raw size")

	tp.Store.mu.RLock()
	rawTotal := tp.Store.rawBytes
	tp.Store.mu.RUnlock()
	require.Equal(t, int64(len(short)+len(long)), rawTotal,
		"Stats.RawBytes must be the sum of both inputs, not twice the first")
}

// TestPutBytes_KeepRawStoresDeltaRoot asserts KeepRaw files the volatile side record as its own
// ordinary root, that canon.Restore can rebuild the redacted input from it, and that the delta
// bytes count toward Stats.Bytes but never toward Stats.RawBytes.
func TestPutBytes_KeepRawStoresDeltaRoot(t *testing.T) {
	tp := newTestStore(t, withCanon(canonStripTimestamp()))
	ctx := context.Background()

	input := []byte(timestampPrefix + "09:11:04\nfirst body\n" + timestampPrefix + "09:11:05\nsecond body\n")

	tp.Store.mu.RLock()
	rawBefore := tp.Store.rawBytes
	tp.Store.mu.RUnlock()

	res, err := tp.Store.PutBytes(ctx, input, PutOptions{Path: "src/log.txt", KeepRaw: true})
	require.NoError(t, err)

	lines := tp.indexLines(t, rootsFile)
	require.Len(t, lines, 2, "KeepRaw must append a second, delta root line")

	var deltaLine map[string]any
	require.NoError(t, json.Unmarshal([]byte(lines[0]), &deltaLine))
	require.Equal(t, deltaToolName, deltaLine["tool"], "the delta record must be filed under the synthetic tool name")

	var contentLine map[string]any
	require.NoError(t, json.Unmarshal([]byte(lines[1]), &contentLine))
	require.NotEmpty(t, contentLine["deltas"], "the content line must point at its delta root")

	// The delta root's own content round-trips back into canon.Delta values.
	deltasField, ok := contentLine["deltas"].(string)
	require.True(t, ok, "the deltas field must be a JSON string, not %T", contentLine["deltas"])
	deltaRoot, err := core.ParseHash(deltasField)
	require.NoError(t, err)
	rc, err := tp.Store.Open(ctx, deltaRoot)
	require.NoError(t, err)
	defer func() { _ = rc.Close() }()
	blob, err := io.ReadAll(rc)
	require.NoError(t, err)

	var decoded []canon.Delta
	require.NoError(t, json.Unmarshal(blob, &deltaJSON{&decoded}))
	require.Len(t, decoded, 2, "both stripped timestamp lines must be recorded")

	tp.Store.mu.RLock()
	rawAfter, bytesAfter := tp.Store.rawBytes, tp.Store.bytesOnDisk
	tp.Store.mu.RUnlock()
	require.Equal(t, rawBefore+int64(len(input)), rawAfter,
		"the delta side record is store overhead and must NOT inflate RawBytes")
	require.Positive(t, bytesAfter, "the delta side record must count toward Stats.Bytes")
	_ = res
}

// deltaJSON decodes the compact {"o","l","c","s"} delta array marshalDeltas writes.
type deltaJSON struct{ out *[]canon.Delta }

// UnmarshalJSON maps the short keys back onto canon.Delta.
func (d *deltaJSON) UnmarshalJSON(b []byte) error {
	var wire []struct {
		O int    `json:"o"`
		L int    `json:"l"`
		C string `json:"c"`
		S string `json:"s"`
	}
	if err := json.Unmarshal(b, &wire); err != nil {
		return err
	}
	for _, w := range wire {
		*d.out = append(*d.out, canon.Delta{Offset: w.O, Len: w.L, Original: w.S, Class: canon.Class(w.C)})
	}
	return nil
}

// TestPutBytes_KeepRawFalseStoresNoDeltas asserts the delta side record is opt-in.
func TestPutBytes_KeepRawFalseStoresNoDeltas(t *testing.T) {
	tp := newTestStore(t, withCanon(canonStripTimestamp()))
	input := []byte(timestampPrefix + "09:11:04\nbody\n")

	_, err := tp.Store.PutBytes(context.Background(), input, PutOptions{Path: "src/log.txt"})
	require.NoError(t, err)

	lines := tp.indexLines(t, rootsFile)
	require.Len(t, lines, 1, "KeepRaw=false must append exactly one root line")
	require.NotContains(t, lines[0], `"deltas"`, "the deltas key must be omitted entirely")
}

// TestPutBytes_EphemeralFlagPersists asserts PutOptions.Ephemeral survives a close and reopen. GC
// reads this flag to decide that a retrieval result is never in-window by age alone (§8.7).
func TestPutBytes_EphemeralFlagPersists(t *testing.T) {
	p := newProject(t)
	tp := openOver(t, p)

	res, err := tp.Store.PutBytes(context.Background(), []byte("ephemeral retrieval result"),
		PutOptions{Path: "src/eph.txt", Ephemeral: true})
	require.NoError(t, err)
	require.Contains(t, tp.indexLines(t, rootsFile)[0], `"eph":true`)
	require.NoError(t, tp.Store.Close())

	reopened := openOver(t, p)
	reopened.Store.mu.RLock()
	entry := reopened.Store.rootIndex[res.Root.Hash]
	reopened.Store.mu.RUnlock()
	require.NotNil(t, entry)
	require.True(t, entry.Eph, "the ephemeral flag must survive a reopen")
}

// TestPutBytes_RedactionBeforeChunking is §13 invariant 7, tested where it actually matters: not
// "the redactor was called" but "the secret is nowhere under objects/". Objects are
// content-addressed and immutable, so a secret that reaches objects/ cannot be removed without
// breaking every root that references its chunk.
func TestPutBytes_RedactionBeforeChunking(t *testing.T) {
	tp := newTestStore(t, withRedactor(fixedRedactor{secret: awsExampleKey}))

	payload := []byte("config dump\naws_access_key_id = " + awsExampleKey + "\nregion = us-east-1\n")
	res, err := tp.Store.PutBytes(context.Background(), payload, PutOptions{Tool: "Bash", Path: "out.txt"})
	require.NoError(t, err)
	require.Equal(t, 1, res.Redacted, "the redactor must report the span it replaced")

	assertNoSecretInObjects(t, tp)
}

// TestPutBytes_RedactionRunsWhenDepsRedactIsNil asserts a nil Deps.Redact defaults to the REAL
// redactor, never to redact.Nop: a nil redactor must never quietly mean "no redaction".
func TestPutBytes_RedactionRunsWhenDepsRedactIsNil(t *testing.T) {
	tp := newTestStore(t, withNilRedactor())

	payload := []byte("config dump\naws_access_key_id = " + awsExampleKey + "\nregion = us-east-1\n")
	res, err := tp.Store.PutBytes(context.Background(), payload, PutOptions{Tool: "Bash", Path: "out.txt"})
	require.NoError(t, err)
	require.Positive(t, res.Redacted, "Deps.Redact=nil must default to redact.New(cfg), not redact.Nop()")

	assertNoSecretInObjects(t, tp)
}

// assertNoSecretInObjects decompresses every stored object and fails if the AWS example key
// appears in any of them.
func assertNoSecretInObjects(t *testing.T, tp *testProject) {
	t.Helper()
	found := 0
	tp.eachObjectPlaintext(t, func(rel string, plain []byte) {
		if bytes.Contains(plain, []byte("AKIA")) {
			found++
			t.Errorf("object %s contains the literal AKIA; §13 invariant 7 forbids a secret reaching objects/", rel)
		}
	})
	require.Zero(t, found)
}

// TestPutBytes_CanonicalizeBeforeChunk asserts the bytes that reach objects/ are the CANONICALIZED
// ones, and that Root records canonical and raw sizes separately.
func TestPutBytes_CanonicalizeBeforeChunk(t *testing.T) {
	tp := newTestStore(t, withCanon(canonUpper()))
	input := []byte("mixed Case Content that the canonicalizer uppercases")

	res, err := tp.Store.PutBytes(context.Background(), input, PutOptions{Path: "src/case.txt"})
	require.NoError(t, err)

	require.Equal(t, int64(len(bytes.ToUpper(input))), res.Root.CanonBytes)
	require.Equal(t, int64(len(input)), res.Root.RawBytes)

	tp.eachObjectPlaintext(t, func(_ string, plain []byte) {
		require.Equal(t, bytes.ToUpper(input), plain, "the stored chunk must be the canonicalized form")
	})
}

// TestPutBytes_CanonFailureFallsBack asserts a canonicalizer error is never fatal: the redacted
// bytes are stored instead, and the fallback is counted.
func TestPutBytes_CanonFailureFallsBack(t *testing.T) {
	tp := newTestStore(t, withCanon(canonFailing()))
	input := []byte("content that must survive a canonicalizer failure")

	res, err := tp.Store.PutBytes(context.Background(), input, PutOptions{Path: "src/fallback.txt"})
	require.NoError(t, err, "a canonicalizer failure must not fail the Put")
	require.Equal(t, int64(len(input)), res.Root.CanonBytes)
	require.Positive(t, tp.counter("store.canon.fallback"))

	rc, err := tp.Store.Open(context.Background(), res.Root.Hash)
	require.NoError(t, err)
	defer func() { _ = rc.Close() }()
	got, err := io.ReadAll(rc)
	require.NoError(t, err)
	require.Equal(t, input, got)
}

// TestPut_ReaderTruncation asserts a stream longer than MaxPutBytes is truncated, not rejected:
// the producer is a hook, and §2.3 permits a hook no exit code but 0.
func TestPut_ReaderTruncation(t *testing.T) {
	if testing.Short() {
		t.Skip("moves 64 MiB; skipped under -short")
	}
	// Large chunks and no compression keep this to a few dozen hashes instead of ~16k.
	tp := newTestStore(t, withChunkSize(4<<20), withCompressionNone())

	res, err := tp.Store.Put(context.Background(), repeatReader(MaxPutBytes+1024), PutOptions{Path: "big.txt"})
	require.NoError(t, err, "an oversized stream must truncate, never error")
	require.True(t, res.Truncated)
	require.Equal(t, int64(MaxPutBytes), res.Root.RawBytes)
}

// repeatReader returns a reader producing exactly n bytes of a repeating pattern, without
// materializing them.
func repeatReader(n int64) io.Reader {
	block := bytes.Repeat([]byte("qompack truncation probe block. "), 1024)
	return io.LimitReader(&cyclicReader{block: block}, n)
}

// cyclicReader yields its block over and over, forever.
type cyclicReader struct {
	block []byte
	off   int
}

// Read fills p from the block, wrapping around at its end.
func (r *cyclicReader) Read(p []byte) (int, error) {
	n := copy(p, r.block[r.off:])
	r.off += n
	if r.off >= len(r.block) {
		r.off = 0
	}
	return n, nil
}

// TestPutBytes_NearDup asserts near-duplicate detection reports the prior root, its similarity and
// the size delta, for two versions of one path.
func TestPutBytes_NearDup(t *testing.T) {
	sig := sketch.Signature{Perms: 128, Mins: []uint64{1, 2, 3, 4}}
	tp := newTestStore(t, withCanon(canonWithSignature(sig)), withMinHash(true, 0.9))
	ctx := context.Background()

	restore := stubJaccard(0.95)
	defer restore()

	v1, err := tp.Store.PutBytes(ctx, fixture(t, "fileread-auth-v1.txt"), PutOptions{Path: "src/auth.ts"})
	require.NoError(t, err)
	require.Nil(t, v1.NearDup, "the first version has no prior to be a near-duplicate of")

	v2, err := tp.Store.PutBytes(ctx, fixture(t, "fileread-auth-v2.txt"), PutOptions{Path: "src/auth.ts"})
	require.NoError(t, err)
	require.NotNil(t, v2.NearDup, "v2 differs from v1 by two lines and must register as a near-duplicate")
	require.Equal(t, v1.Root.Hash, v2.NearDup.PriorRoot)
	require.GreaterOrEqual(t, v2.NearDup.Jaccard, 0.9)
	require.Positive(t, v2.NearDup.DeltaBytes)
}

// TestPutBytes_NoNearDupForDistinctPaths asserts near-duplicate detection is scoped to one path:
// two unrelated payloads on different paths are never near-duplicates of each other.
func TestPutBytes_NoNearDupForDistinctPaths(t *testing.T) {
	sig := sketch.Signature{Perms: 128, Mins: []uint64{1, 2, 3, 4}}
	tp := newTestStore(t, withCanon(canonWithSignature(sig)), withMinHash(true, 0.9))
	ctx := context.Background()

	restore := stubJaccard(0.99)
	defer restore()

	_, err := tp.Store.PutBytes(ctx, []byte("alpha content"), PutOptions{Path: "src/alpha.txt"})
	require.NoError(t, err)
	res, err := tp.Store.PutBytes(ctx, []byte("beta content"), PutOptions{Path: "src/beta.txt"})
	require.NoError(t, err)
	require.Nil(t, res.NearDup, "near-duplicate detection must be scoped to a single path")
}

// TestPutBytes_NoNearDupWhenMinHashDisabled asserts the feature honours its config switch.
func TestPutBytes_NoNearDupWhenMinHashDisabled(t *testing.T) {
	sig := sketch.Signature{Perms: 128, Mins: []uint64{1, 2, 3, 4}}
	tp := newTestStore(t, withCanon(canonWithSignature(sig)), withMinHash(false, 0.9))
	ctx := context.Background()

	restore := stubJaccard(0.99)
	defer restore()

	_, err := tp.Store.PutBytes(ctx, fixture(t, "fileread-auth-v1.txt"), PutOptions{Path: "src/auth.ts"})
	require.NoError(t, err)
	res, err := tp.Store.PutBytes(ctx, fixture(t, "fileread-auth-v2.txt"), PutOptions{Path: "src/auth.ts"})
	require.NoError(t, err)
	require.Nil(t, res.NearDup)
}

// stubJaccard swaps the near-duplicate similarity seam and returns a restore func.
//
// The seam exists only because internal/sketch is still an SP-01 stub whose Jaccard reports a flat
// 0 (Rule W-2), which would otherwise make near-duplicate detection untestable until SP-03 merges
// later in this same wave.
func stubJaccard(v float64) func() {
	prev := signatureJaccard
	signatureJaccard = func(a, b sketch.Signature) float64 { return v }
	return func() { signatureJaccard = prev }
}

// TestPutBytes_ChunkerDegradedGuard asserts splitChecked's data-loss guard: a chunker that returns
// nothing for non-empty input (which the SP-01 stub does) must still store the content, as one
// chunk, and must say so.
func TestPutBytes_ChunkerDegradedGuard(t *testing.T) {
	tp := newTestStore(t, withStubChunker())
	input := []byte("content the stub chunker refuses to split")

	res, err := tp.Store.PutBytes(context.Background(), input, PutOptions{Path: "src/degraded.txt"})
	require.NoError(t, err)
	require.Len(t, res.Root.Chunks, 1, "the guard must store the whole input as a single chunk")
	require.Equal(t, int64(1), tp.counter("store.chunker.degraded"))

	rc, err := tp.Store.Open(context.Background(), res.Root.Hash)
	require.NoError(t, err)
	defer func() { _ = rc.Close() }()
	got, err := io.ReadAll(rc)
	require.NoError(t, err)
	require.Equal(t, input, got, "no bytes may be lost when the chunker degrades")
}

// TestConcurrentPut asserts the store is safe for concurrent use and that concurrency does not
// change what gets stored: the object set must match the single-threaded result exactly.
func TestConcurrentPut(t *testing.T) {
	const goroutines, perGoroutine = 8, 50

	payloads := make([][]byte, 0, goroutines*perGoroutine)
	for g := 0; g < goroutines; g++ {
		for i := 0; i < perGoroutine; i++ {
			// Every fifth payload is shared across all goroutines, so ~20 % of writes collide on
			// the same content and exercise the concurrent-identical-object path.
			if i%5 == 0 {
				payloads = append(payloads, []byte(fmt.Sprintf("shared payload %d", i)))
				continue
			}
			payloads = append(payloads, []byte(fmt.Sprintf("payload g=%d i=%d %s", g, i, strings.Repeat("x", 100))))
		}
	}

	concurrent := newTestStore(t)
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < perGoroutine; i++ {
				_, err := concurrent.Store.PutBytes(context.Background(), payloads[g*perGoroutine+i],
					PutOptions{Tool: "Bash", Path: fmt.Sprintf("src/g%d.txt", g)})
				require.NoError(t, err)
			}
		}(g)
	}
	wg.Wait()

	serial := newTestStore(t)
	for g := 0; g < goroutines; g++ {
		for i := 0; i < perGoroutine; i++ {
			_, err := serial.Store.PutBytes(context.Background(), payloads[g*perGoroutine+i],
				PutOptions{Tool: "Bash", Path: fmt.Sprintf("src/g%d.txt", g)})
			require.NoError(t, err)
		}
	}

	require.Equal(t, serial.objectPaths(t), concurrent.objectPaths(t),
		"concurrent puts must produce exactly the object set the serial order produces")
}

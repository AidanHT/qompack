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

// TestPutBytes_GlobalDedup asserts an edited file costs only its edit, against the REAL FastCDC
// chunker and the REAL canonicalizers.
//
// Measured at the V2 checkpoint, production chunk parameters (1 KiB / 4 KiB / 16 KiB):
//
//	version  chunks  split as               changed bytes   chunks it spans  novel  reused
//	v1        4      4422/5752/7714/5470    (whole file)     4                4      0
//	v2        4      4438/5752/7714/5470    [435, 563)       1                1      3
//	v3        4      4648/5778/7714/5470    [910, 5057)      2                2      2
//
// Objects on disk: 4 after v1, 7 after v3.
//
// # Why the previous two bounds are gone
//
// They were "novel[i] < novel[0]/4" and "total < 1.6 x the objects v1 left", and both were
// calibrated against the ~256-byte-average chunker this package injected while internal/chunk was
// an SP-01 stub. There v1 is 65 chunks, so one novel chunk is 1.5 % of a first read and both
// bounds have room. At production parameters v1 is FOUR chunks: one novel chunk is 25 %, v2's
// single perturbed chunk fails "< novel[0]/4" outright, and v3 leaves 1.75x v1's objects. Neither
// figure was ever a statement about deduplication — both are arithmetic about how finely this
// fixture happens to be cut, and a bound that moves with the chunk size is not a bound on dedup.
//
// # What is asserted instead, quoting §6.1
//
//	"Content-defined chunking cuts at boundaries determined by a rolling hash of a sliding
//	 window, so an insertion perturbs one chunk and the rest realign. […] Four reads of a
//	 2,000-line file collapse to one chunk set plus three near-empty reference lists."
//
// Two claims, now asserted directly rather than through a chunk-count proxy. "The rest realign"
// becomes: an edit may rewrite ONLY the chunks whose byte range it overlaps, where the range comes
// from diffing the two fixtures — a bound no chunker can satisfy merely by cutting more coarsely.
// "Three near-empty reference lists" becomes: the two edits together cost less than one more read
// of the file. Both hold exactly, with no slack, on the numbers above.
//
// v4 is a 240-line rewrite that also grows the file 19 %, so it is not the small-edit case §6.1
// describes; TestPutBytes_LargeRewriteStillSharesChunks covers it and explains what it costs.
func TestPutBytes_GlobalDedup(t *testing.T) {
	tp := newTestStore(t)
	ctx := context.Background()

	names := []string{"fileread-auth-v1.txt", "fileread-auth-v2.txt", "fileread-auth-v3.txt"}
	var roots []core.Hash
	var results []PutResult
	var afterV1 int
	for i, name := range names {
		res, err := tp.Store.PutBytes(ctx, fixture(t, name), PutOptions{Tool: "FileRead", Path: "src/auth.ts"})
		require.NoError(t, err)
		roots = append(roots, res.Root.Hash)
		results = append(results, res)
		if i == 0 {
			afterV1 = len(tp.objectPaths(t))
		}
	}

	require.Len(t, uniqueHashes(roots), 3, "three different versions must produce three different roots")
	require.Equal(t, len(results[0].Root.Chunks), results[0].Novel,
		"the first read of new content must write every chunk it produced")
	require.Equal(t, results[0].Novel, afterV1, "fixture sanity: v1's chunks are the only objects on disk")

	for i := 1; i < len(results); i++ {
		res := results[i]
		require.Equal(t, len(res.Root.Chunks), res.Novel+res.Reused,
			"every chunk of version %d must be accounted for as novel or reused", i+1)
		require.Less(t, res.Novel, results[0].Novel,
			"a small edit must cost strictly less than a first read; version %d wrote %d novel chunks vs %d",
			i+1, res.Novel, results[0].Novel)

		lo, hi := changedSpan(fixture(t, names[i-1]), fixture(t, names[i]))
		spanned := chunksSpanning(res.Root.Chunks, lo, hi)
		require.LessOrEqual(t, res.Novel, spanned,
			"§6.1: an edit perturbs the chunks it overlaps and the rest realign. Version %d changed "+
				"bytes [%d,%d), which its own chunking covers with %d chunks, but %d were rewritten",
			i+1, lo, hi, spanned, res.Novel)
	}

	total := len(tp.objectPaths(t))
	require.Less(t, total-afterV1, afterV1,
		"§6.1's near-empty reference lists: the two small edits together must cost less than one more "+
			"read of the file; they added %d objects against %d for the file itself", total-afterV1, afterV1)
}

// changedSpan returns the half-open byte range of b that differs from a: everything between the
// two versions' common prefix and their common suffix. It is a conservative OVER-estimate of what
// an edit touched — a coincidental match inside the edit only widens it — which is the safe
// direction for a bound stated as "at most the chunks this span covers".
func changedSpan(a, b []byte) (lo, hi int64) {
	p := 0
	for p < len(a) && p < len(b) && a[p] == b[p] {
		p++
	}
	s := 0
	for s < len(a)-p && s < len(b)-p && a[len(a)-1-s] == b[len(b)-1-s] {
		s++
	}
	return int64(p), int64(len(b) - s)
}

// chunksSpanning counts how many of chunks overlap the half-open byte range [lo, hi). Offsets are
// accumulated from the lengths, which is exactly how a root's chunk list tiles its content.
func chunksSpanning(chunks []core.ChunkRef, lo, hi int64) int {
	n, off := 0, int64(0)
	for _, c := range chunks {
		end := off + int64(c.Len)
		if off < hi && lo < end {
			n++
		}
		off = end
	}
	return n
}

// TestPutBytes_LargeRewriteStillSharesChunks asserts a large rewrite reuses the chunks it did not
// touch rather than degenerating into a second full copy the way a fixed-size splitter would — and
// pins the one condition, measured here, under which it provably cannot.
//
// # Arm 1, the claim
//
// It needs a file with enough chunks for the question to be about resynchronization at all. ~97 KB
// of source-shaped text is 25 chunks at production parameters; rewriting 240 contiguous lines in
// the middle costs 4 novel chunks and reuses 21. That is §6.1's promise, at the scale §6.1 states
// it ("a 2,000-line file").
//
// # Arm 2, the SP-06 fixture's v3 to v4, which reuses nothing
//
// Measured at this checkpoint: the 23 KB fixture is only four chunks — v3 splits
// 4648/5778/7714/5470 — and v4's rewrite removes every rolling-hash cut point from its first
// 16 KB, so the chunker falls back to its MAX-size clamp and v4 splits 16384/7674/3945. A max-size
// cut is positional by definition, so every boundary after it shifts and nothing downstream can
// realign. §6.1 promises that "an insertion perturbs one chunk and the rest realign", which is a
// claim about a rolling-hash cut; a region that no longer has one is outside it, and asserting
// reuse there would be asserting something the design does not promise.
//
// So arm 2 pins the MECHANISM rather than the outcome: the clamped first chunk, and the fact that
// every chunk is still accounted for. If SP-04's chunker ever stops clamping here, the first
// assertion fails and this comment is what the next reader needs.
func TestPutBytes_LargeRewriteStillSharesChunks(t *testing.T) {
	tp := newTestStore(t)
	ctx := context.Background()

	const lines, rewriteFrom, rewrittenLines = 2000, 1000, 240
	before := rewritableSource(lines, -1, -1)
	after := rewritableSource(lines, rewriteFrom, rewriteFrom+rewrittenLines)

	base, err := tp.Store.PutBytes(ctx, before, PutOptions{Tool: "FileRead", Path: "src/wide.ts"})
	require.NoError(t, err)
	require.Greater(t, len(base.Root.Chunks), 20,
		"fixture sanity: the claim is only about resynchronization when there are cut points to resync on")

	rewritten, err := tp.Store.PutBytes(ctx, after, PutOptions{Tool: "FileRead", Path: "src/wide.ts"})
	require.NoError(t, err)
	require.NotEqual(t, base.Root.Hash, rewritten.Root.Hash)
	require.Greater(t, rewritten.Reused, rewritten.Novel,
		"a 240-line rewrite must reuse MORE chunks than it rewrites; got %d reused against %d novel",
		rewritten.Reused, rewritten.Novel)

	v3, err := tp.Store.PutBytes(ctx, fixture(t, "fileread-auth-v3.txt"), PutOptions{Tool: "FileRead", Path: "src/auth.ts"})
	require.NoError(t, err)
	v4, err := tp.Store.PutBytes(ctx, fixture(t, "fileread-auth-v4.txt"), PutOptions{Tool: "FileRead", Path: "src/auth.ts"})
	require.NoError(t, err)
	require.NotEqual(t, v3.Root.Hash, v4.Root.Hash)
	require.Equal(t, len(v4.Root.Chunks), v4.Novel+v4.Reused, "every chunk must be accounted for")
	require.Equal(t, tp.Cfg.Store.Chunk.Max, v4.Root.Chunks[0].Len,
		"v4 reuses nothing because its first chunk is MAX-clamped, not because boundary stability "+
			"is broken; if the clamp no longer fires, re-derive this test's second arm")
}

// rewritableSource builds `lines` lines of deterministic source-shaped text, rewriting the
// half-open line range [from, to) when from is non-negative. ~97 KB at 2 000 lines, which is 25
// chunks at production parameters — the scale §6.1's "2,000-line file" describes, and enough for
// an edit's boundary shift to have somewhere to resynchronize.
func rewritableSource(lines, from, to int) []byte {
	var b strings.Builder
	for i := 0; i < lines; i++ {
		if from >= 0 && i >= from && i < to {
			fmt.Fprintf(&b, "  const patched_%04d = refreshToken(%d, \"rev1\");\n", i, i*7)
			continue
		}
		switch i % 4 {
		case 0:
			fmt.Fprintf(&b, "export function handler_%04d(req: Session): Token {\n", i)
		case 1:
			fmt.Fprintf(&b, "  const scope = resolveScope(req, %d, \"m/%04d\");\n", i*13, i)
		case 2:
			fmt.Fprintf(&b, "  if (!scope.valid) throw new AuthError(\"m:%04d denied\");\n", i)
		default:
			fmt.Fprintf(&b, "  return issue(scope, %d);\n}\n\n", i*31)
		}
	}
	return []byte(b.String())
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
		// "platform: " is the one prefix devtool's stubskips check permits for a skip that hides no
		// missing work (tools/devtool/stubskips.go). Any other reason is a hard lint failure, and
		// this skip is only ever reached under -short — which the default `devtool test` does not
		// pass, so it has never fired and the non-conformant message was never noticed.
		t.Skip("platform: moves 64 MiB, skipped under -short")
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
// the size delta, for two versions of one path — against REAL MinHash signatures.
//
// All three near-dup tests used to inject a canonicalizer carrying a hand-built four-minima
// signature and swap a package-level signatureJaccard variable for a constant, because
// sketch.Signature.Jaccard reported a flat 0 while sketch was an SP-01 stub. Between them the
// similarity, the signatures and the comparison were all fabricated, so what was left to assert
// was that PutResult copied three fields out of a value the test had supplied. The seam is gone
// with the stub that needed it: nearDup calls prior.Sig.Jaccard directly, and canon computes the
// signatures from the fixtures. Measured here, v1 to v2 scores 0.9922 against the configured 0.9
// threshold, with a 16-byte canonical delta.
func TestPutBytes_NearDup(t *testing.T) {
	tp := newTestStore(t, withMinHash(true, 0.9))
	ctx := context.Background()

	v1, err := tp.Store.PutBytes(ctx, fixture(t, "fileread-auth-v1.txt"), PutOptions{Path: "src/auth.ts"})
	require.NoError(t, err)
	require.Nil(t, v1.NearDup, "the first version has no prior to be a near-duplicate of")
	require.NotZero(t, v1.Signature.Perms, "fixture sanity: MinHash must actually have run")

	v2, err := tp.Store.PutBytes(ctx, fixture(t, "fileread-auth-v2.txt"), PutOptions{Path: "src/auth.ts"})
	require.NoError(t, err)
	require.NotNil(t, v2.NearDup, "v2 differs from v1 by two lines and must register as a near-duplicate")
	require.Equal(t, v1.Root.Hash, v2.NearDup.PriorRoot)
	require.GreaterOrEqual(t, v2.NearDup.Jaccard, 0.9)
	require.Less(t, v2.NearDup.Jaccard, 1.0, "two different files must not score as identical")
	require.Positive(t, v2.NearDup.DeltaBytes)
}

// TestPutBytes_NoNearDupForDistinctPaths asserts near-duplicate detection is scoped to one path.
//
// The two payloads are the SAME near-duplicate pair TestPutBytes_NearDup uses, put on DIFFERENT
// paths. That is the whole strength of the test: their real similarity is 0.99, comfortably over
// the threshold, so a nil result can only come from the path scoping. Two unrelated payloads —
// which is what this test used to compare, under a stubbed similarity of 0.99 — would report nil
// under real MinHash whether the scoping existed or not.
func TestPutBytes_NoNearDupForDistinctPaths(t *testing.T) {
	tp := newTestStore(t, withMinHash(true, 0.9))
	ctx := context.Background()

	_, err := tp.Store.PutBytes(ctx, fixture(t, "fileread-auth-v1.txt"), PutOptions{Path: "src/alpha.ts"})
	require.NoError(t, err)
	res, err := tp.Store.PutBytes(ctx, fixture(t, "fileread-auth-v2.txt"), PutOptions{Path: "src/beta.ts"})
	require.NoError(t, err)
	require.Nil(t, res.NearDup, "near-duplicate detection must be scoped to a single path")
}

// TestPutBytes_NoNearDupWhenMinHashDisabled asserts the feature honours its config switch, on the
// pair that would otherwise score 0.99.
func TestPutBytes_NoNearDupWhenMinHashDisabled(t *testing.T) {
	tp := newTestStore(t, withMinHash(false, 0.9))
	ctx := context.Background()

	first, err := tp.Store.PutBytes(ctx, fixture(t, "fileread-auth-v1.txt"), PutOptions{Path: "src/auth.ts"})
	require.NoError(t, err)
	require.Zero(t, first.Signature.Perms, "a disabled MinHash must compute no signature at all")
	res, err := tp.Store.PutBytes(ctx, fixture(t, "fileread-auth-v2.txt"), PutOptions{Path: "src/auth.ts"})
	require.NoError(t, err)
	require.Nil(t, res.NearDup)
}

// TestPutBytes_ChunkerDegradedGuard asserts splitChecked's data-loss guard: a chunker that returns
// nothing for non-empty input must still store the content, as one chunk, and must say so.
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

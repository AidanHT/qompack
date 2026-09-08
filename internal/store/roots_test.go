package store

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/sketch"
	"github.com/qompack/qompack/internal/tokens"
)

// TestMarshalRootLine_FieldOrderIsFixed pins the roots.jsonl record's key order. The order is part
// of the format, not an accident of struct layout: testdata/golden/store/roots.jsonl compares
// byte-for-byte, so a marshaller that reordered fields would break every golden at once.
func TestMarshalRootLine_FieldOrderIsFixed(t *testing.T) {
	rl := rootEntry{
		Root: Root{
			Hash:       core.HashBytes(core.DomainRoot, []byte("root")),
			Chunks:     []ChunkRef{{Hash: core.HashBytes(core.DomainChunk, []byte("a")), Len: 4096}},
			CanonBytes: 23904,
			RawBytes:   24188,
			Tokens:     5942,
		},
		TS: 1734128400123, Tool: "FileRead", Path: "src/auth.ts", Class: uint8(tokens.ClassCode),
	}

	line := string(marshalRootLine(rl))
	require.True(t, strings.HasSuffix(line, "\n"), "every record must be newline-terminated")

	wantOrder := []string{`"v":`, `"root":`, `"ts":`, `"tool":`, `"path":`, `"raw":`, `"canon":`, `"tokens":`, `"class":`, `"chunks":`}
	at := -1
	for _, key := range wantOrder {
		i := strings.Index(line, key)
		require.Greater(t, i, at, "key %s must appear after the previous one; got %s", key, line)
		at = i
	}
	require.Contains(t, line, `"class":1`, "ClassCode's persisted ordinal is 1")
	require.Contains(t, line, `{"h":"sha256:`, "chunk entries use the short h/n keys")
	require.NotContains(t, line, `"eph"`, "eph must be omitted when false")
	require.NotContains(t, line, `"sig"`, "sig must be omitted when absent")
	require.NotContains(t, line, `"deltas"`, "deltas must be omitted when absent")
}

// TestMarshalRootLine_OmitsEmptyOptionalsButKeepsSetOnes asserts the three optional keys appear
// exactly when they carry information.
func TestMarshalRootLine_OmitsEmptyOptionalsButKeepsSetOnes(t *testing.T) {
	rl := rootEntry{
		Root:   Root{Hash: core.HashBytes(core.DomainRoot, []byte("r"))},
		Eph:    true,
		Deltas: core.HashBytes(core.DomainRoot, []byte("d")),
	}
	line := string(marshalRootLine(rl))
	require.Contains(t, line, `"eph":true`)
	require.Contains(t, line, `"deltas":"sha256:`)
}

// TestAppendJSONString_MatchesEncodingJSONWithoutHTMLEscaping asserts the hand-written string
// escaper produces exactly what encoding/json does with SetEscapeHTML(false) — which is what every
// other JSON writer in this codebase uses, so an index line and a paths.AppendJSONL line agree.
func TestAppendJSONString_MatchesEncodingJSONWithoutHTMLEscaping(t *testing.T) {
	cases := []string{
		"", "plain", `with "quotes"`, `back\slash`, "tab\tnewline\ncr\r",
		"html <b>&</b> chars", "unicode: héllo wörld ☃", "control\x00\x01\x1f",
		"line sep", "para sep", "emoji 🎯",
	}
	for _, in := range cases {
		got := string(appendJSONString(nil, in))
		want := encodingJSONString(t, in)
		require.Equal(t, want, got, "escaping of %q must match encoding/json", in)
	}
}

// encodingJSONString renders s the way encoding/json does with HTML escaping disabled.
func encodingJSONString(t *testing.T, s string) string {
	t.Helper()
	var sb strings.Builder
	enc := json.NewEncoder(&sb)
	enc.SetEscapeHTML(false)
	require.NoError(t, enc.Encode(s))
	return strings.TrimSuffix(sb.String(), "\n")
}

// TestAppendJSONString_ReplacesInvalidUTF8 asserts invalid bytes become U+FFFD rather than being
// emitted raw, which would produce a line no JSON reader could parse.
func TestAppendJSONString_ReplacesInvalidUTF8(t *testing.T) {
	got := string(appendJSONString(nil, "bad\xffbyte"))
	require.Equal(t, encodingJSONString(t, "bad\xffbyte"), got)
	require.True(t, json.Valid([]byte(got)))
}

// TestParseRootLine_RoundTrip asserts a marshalled record parses back to the same values.
func TestParseRootLine_RoundTrip(t *testing.T) {
	want := rootEntry{
		Root: Root{
			Hash: core.HashBytes(core.DomainRoot, []byte("root")),
			Chunks: []ChunkRef{
				{Hash: core.HashBytes(core.DomainChunk, []byte("a")), Len: 4096},
				{Hash: core.HashBytes(core.DomainChunk, []byte("b")), Len: 3771},
			},
			CanonBytes: 23904, RawBytes: 24188, Tokens: 5942,
		},
		TS: 1734128400123, Tool: "Bash", Path: "src/<weird>&name.ts",
		Class: uint8(tokens.ClassJSON), Eph: true,
	}

	got, tombstone, root, err := parseRootLine(marshalRootLine(want))
	require.NoError(t, err)
	require.False(t, tombstone)
	require.Equal(t, want.Root.Hash, root)
	require.Equal(t, want.Root, got.Root)
	require.Equal(t, want.TS, got.TS)
	require.Equal(t, want.Tool, got.Tool)
	require.Equal(t, want.Path, got.Path, "an unescaped < in a path must survive verbatim")
	require.Equal(t, want.Class, got.Class)
	require.True(t, got.Eph)
}

// TestParseRootLine_Tombstone asserts a GC tombstone is recognized as such.
func TestParseRootLine_Tombstone(t *testing.T) {
	h := core.HashBytes(core.DomainRoot, []byte("collected"))
	_, tombstone, root, err := parseRootLine(marshalGCTombstone(h, 1734131000000))
	require.NoError(t, err)
	require.True(t, tombstone)
	require.Equal(t, h, root)
}

// TestAppendGCTombstone_RetiresRootAppendOnly asserts collecting a root drops it from the index and
// decrements its chunks' refcounts, while leaving the ORIGINAL line untouched on disk: index files
// are append-only, so a root is retired by appending, never by rewriting (§7.4).
func TestAppendGCTombstone_RetiresRootAppendOnly(t *testing.T) {
	tp := newTestStore(t)
	ctx := context.Background()

	res, err := tp.Store.PutBytes(ctx, []byte("content that will be collected"), PutOptions{Path: "src/gone.txt"})
	require.NoError(t, err)
	original := tp.indexLines(t, rootsFile)[0]

	require.NoError(t, tp.Store.appendGCTombstone(res.Root.Hash))

	_, err = tp.Store.GetRoot(ctx, res.Root.Hash)
	require.ErrorIs(t, err, core.ErrNotFound, "a tombstoned root must read as not-found")

	lines := tp.indexLines(t, rootsFile)
	require.Len(t, lines, 2)
	require.Equal(t, original, lines[0], "the original record must be left byte-identical on disk")
	require.Contains(t, lines[1], `"op":"gc"`)

	for _, c := range res.Root.Chunks {
		require.Zero(t, tp.Store.ApproxRefs(c.Hash), "a collected root's chunks must lose their reference")
	}
}

// TestLoadRoots_TombstoneSurvivesReopen asserts a retired root stays retired after a reopen.
func TestLoadRoots_TombstoneSurvivesReopen(t *testing.T) {
	p := newProject(t)
	tp := openOver(t, p)
	ctx := context.Background()

	res, err := tp.Store.PutBytes(ctx, []byte("collected before reopen"), PutOptions{Path: "src/gone.txt"})
	require.NoError(t, err)
	require.NoError(t, tp.Store.appendGCTombstone(res.Root.Hash))
	require.NoError(t, tp.Store.Close())

	reopened := openOver(t, p)
	_, err = reopened.Store.GetRoot(ctx, res.Root.Hash)
	require.ErrorIs(t, err, core.ErrNotFound)
}

// TestLoadRoots_UnknownClassDegradesToProse asserts a class ordinal from a future format version is
// clamped rather than trusted, and is counted so the situation stays visible.
func TestLoadRoots_UnknownClassDegradesToProse(t *testing.T) {
	p := newProject(t)
	tp := openOver(t, p)
	res, err := tp.Store.PutBytes(context.Background(), []byte("known class"), PutOptions{Path: "src/c.txt"})
	require.NoError(t, err)
	require.NoError(t, tp.Store.Close())

	future := rootEntry{
		Root:  Root{Hash: core.HashBytes(core.DomainRoot, []byte("future")), Chunks: res.Root.Chunks},
		Class: 99,
	}
	appendRawIndexLine(t, tp, rootsFile, strings.TrimRight(string(marshalRootLine(future)), "\n"))

	reopened := openOver(t, p)
	require.Equal(t, int64(1), reopened.counter("store.index.badclass"))

	reopened.Store.mu.RLock()
	entry := reopened.Store.rootIndex[future.Root.Hash]
	reopened.Store.mu.RUnlock()
	require.NotNil(t, entry)
	require.Equal(t, uint8(tokens.ClassProse), entry.Class)
}

// TestEncodeSignature_EmitsValidOmitsUnmarshalable states encodeSignature's real contract, now
// that internal/sketch is real.
//
// This test used to be called TestEncodeSignature_OmittedWhileSketchIsAStub, and it went on
// passing for an entirely different reason once SP-03 landed. Its second case is
// Signature{Perms: 128, Mins: {1,2,3}} — a value MinHash cannot produce, because it declares 128
// permutations and carries three minima. While MarshalBinary reported ErrNotImplemented for
// everything, that case proved the stub was a stub; against the real codec it proves that a
// MALFORMED signature is refused, which is a different and better assertion — but the name said
// the opposite, so nothing recorded that the interesting half had gone missing.
//
// The interesting half is the one restored first: a VALID signature is emitted. Without it,
// "encodeSignature omits" is compatible with an implementation that omits everything, which is
// precisely the state SP-06 shipped in and could not distinguish.
func TestEncodeSignature_EmitsValidOmitsUnmarshalable(t *testing.T) {
	valid := sketch.MinHash([]byte("content long enough to shingle at the default width"),
		sketch.MinHashOptions{Enabled: true, Permutations: 128})
	require.Equal(t, uint16(128), valid.Perms, "fixture sanity: the real MinHash must produce a signature")

	enc, ok := encodeSignature(valid)
	require.True(t, ok, "a signature the real MinHash produced must be RECORDED, not omitted")
	raw, err := base64.StdEncoding.DecodeString(enc)
	require.NoError(t, err)
	var back sketch.Signature
	require.NoError(t, back.UnmarshalBinary(raw))
	require.Equal(t, valid, back, "what is written must be exactly what reads back")

	_, ok = encodeSignature(sketch.Signature{})
	require.False(t, ok, "an empty signature has nothing to record")

	// Perms and Mins disagreeing is not producible by MinHash but is constructible by hand and
	// decodable from a forged record field. It must cost the KEY, never the write: a Put whose
	// near-duplicate metadata cannot be serialized still has content to store (§12.3).
	_, ok = encodeSignature(sketch.Signature{Perms: 128, Mins: []uint64{1, 2, 3}})
	require.False(t, ok, "a signature sketch's own codec refuses must be omitted rather than written")
}

// TestAppendRoot_MalformedSignatureCostsTheKeyNotTheWrite is the same contract one level up: the
// omission has to be invisible to the caller, because a failed Put would mean a tool result was
// lost over an optimization that did not apply.
func TestAppendRoot_MalformedSignatureCostsTheKeyNotTheWrite(t *testing.T) {
	tp := newTestStore(t)

	line := marshalRootLine(rootEntry{
		Root: Root{Hash: core.HashBytes(core.DomainRoot, []byte("malformed sig")), CanonBytes: 4, RawBytes: 4},
		TS:   tp.Store.now(), Tool: "FileRead", Path: "src/a.ts",
		Sig: sketch.Signature{Perms: 128, Mins: []uint64{1, 2, 3}},
	})
	require.NotContains(t, string(line), `"sig"`,
		"an unserializable signature must drop the key, leaving a well-formed record")
	require.Contains(t, string(line), `"root"`, "and the rest of the line must be written normally")
}

// ── the versioned roots-index goldens ────────────────────────────────────────────────────────

// The two goldens under testdata/golden/store/ that this reader must speak, and the rule they
// encode together.
//
// roots.jsonl is what SP-06 wrote and is FROZEN: every line says v=1, and that is the shape every
// store on disk today is written in. roots.v2.jsonl is its versioned SUCCESSOR — the same reader,
// one file later — carrying a v=1 line beside a v=2 line that declares a delta base
// (indexRecordVersionBase, SP-20 invariant 6). The successor does not replace the older golden and
// must never be regenerated from it: the whole point of keeping both is that adding a record
// version did not cost the old one its reader.
//
// Both directions are pinned below, and both matter. A reader that lost v=1 would strand every
// index on disk; a reader that never exercised v=2 would leave the additive bump unproven.
const (
	rootsGoldenV1 = "roots.jsonl"
	rootsGoldenV2 = "roots.v2.jsonl"
)

// readRootsGolden returns one golden's bytes with CRLF normalized to LF, for the same reason
// goldenIndexFile normalizes on comparison: a Windows checkout can hand back CRLF for a committed
// .jsonl, and a line-ending difference has nothing to do with the reader under test.
func readRootsGolden(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "testdata", "golden", "store", name))
	require.NoError(t, err, "golden %s is missing", name)
	return bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n"))
}

// loadRootsGolden plants one golden as a project's index/roots.jsonl and opens a REAL store over
// it, returning a copy of the store's loaded root index. The golden therefore travels the
// production reader — loadRoots, parseRootLine, indexRootLocked — rather than a parser called
// directly.
func loadRootsGolden(t *testing.T, name string) map[core.Hash]rootEntry {
	t.Helper()
	p := newProject(t)
	l := paths.Of(p.Root)
	require.NoError(t, os.MkdirAll(paths.Long(l.Index), 0o700))
	require.NoError(t, os.WriteFile(paths.Long(filepath.Join(l.Index, rootsFile)),
		readRootsGolden(t, name), 0o600))

	tp := openOver(t, p)
	tp.Store.mu.RLock()
	defer tp.Store.mu.RUnlock()
	out := make(map[core.Hash]rootEntry, len(tp.Store.rootIndex))
	for h, e := range tp.Store.rootIndex {
		out[h] = *e
	}
	return out
}

// requireRootsGoldenRoundTrips asserts every line of a golden survives parse → marshal unchanged,
// byte for byte.
//
// This is what makes a golden evidence about the WRITER rather than a hand-typed fixture that
// merely happens to parse. A golden the reader accepts but the writer would never emit — a key out
// of order, an optional field spelled with a zero value, a "v" that disagrees with what
// rootRecordVersion computes for its own content — passes a load-only test and pins nothing.
func requireRootsGoldenRoundTrips(t *testing.T, name string) {
	t.Helper()
	for i, line := range bytes.Split(bytes.TrimRight(readRootsGolden(t, name), "\n"), []byte("\n")) {
		rl, tombstone, _, err := parseRootLine(line)
		require.NoError(t, err, "%s line %d does not parse", name, i+1)
		require.False(t, tombstone, "%s line %d: these goldens carry no tombstones", name, i+1)
		got := bytes.TrimRight(marshalRootLine(rl), "\n")
		require.Equal(t, string(line), string(got),
			"%s line %d is not what the writer emits for its own content; re-derive the golden "+
				"from marshalRootLine rather than loosening the reader", name, i+1)
	}
}

// TestGolden_RootsV2IndexLoadsThroughTheReader wires the versioned successor golden: it plants
// roots.v2.jsonl as a real store's index and asserts the v=2 record shape came back intact.
//
// The v=2 line is a delta side record — the synthetic «deltas» tool, no path, no chunks, and the
// declared "base" that is the entire reason the version moved. Base is the field a reader must not
// silently drop: a delta whose base is lost supports no recovery claim at all (SP-20 invariant 6),
// and the loss would go unnoticed because every other field on the line still reads correctly.
func TestGolden_RootsV2IndexLoadsThroughTheReader(t *testing.T) {
	index := loadRootsGolden(t, rootsGoldenV2)
	require.Len(t, index, 2, "the successor golden carries one v=1 record and one v=2 record")

	var legacy, withBase rootEntry
	var haveLegacy, haveBase bool
	for _, e := range index {
		if e.Base.IsZero() {
			legacy, haveLegacy = e, true
			continue
		}
		withBase, haveBase = e, true
	}
	require.True(t, haveLegacy, "the v=1 record beside it must still load")
	require.True(t, haveBase, "the v=2 record must load")

	require.Equal(t, "src/a.ts", legacy.Path)
	require.Equal(t, "Bash", legacy.Tool)
	require.Len(t, legacy.Root.Chunks, 1, "a content record carries its chunk list")

	require.Equal(t, deltaToolName, withBase.Tool, "a delta side record is filed under the synthetic tool")
	require.Empty(t, withBase.Path, "a delta side record belongs to no path")
	require.Empty(t, withBase.Root.Chunks, "and carries no chunks of its own")
	require.Equal(t, legacy.Root.Hash, withBase.Base,
		"the declared base must point at the record it reconstructs")
	require.Equal(t, int64(indexRecordVersionBase), rootRecordVersion(withBase),
		"a record carrying a base declares the bumped version")
	require.Equal(t, int64(indexRecordVersion), rootRecordVersion(legacy),
		"and one that does not stays v=1, which is what keeps the older golden readable")

	requireRootsGoldenRoundTrips(t, rootsGoldenV2)
}

// TestGolden_RootsV1IndexStillLoadsByteIdentically is the other half, and the half whose breakage
// would be the expensive one: the SP-06 golden — three multi-chunk content records carrying MinHash
// signatures — must still load through the SAME reader and still re-marshal to the same bytes.
//
// A versioned successor is only worth having if the predecessor survives it. Reading roots.v2.jsonl
// alone would prove the new shape works while saying nothing about the shape every store on disk is
// actually written in, which is the one that cannot be regenerated.
func TestGolden_RootsV1IndexStillLoadsByteIdentically(t *testing.T) {
	golden := readRootsGolden(t, rootsGoldenV1)
	lines := bytes.Split(bytes.TrimRight(golden, "\n"), []byte("\n"))
	require.NotEmpty(t, lines, "fixture sanity: the frozen golden must have content")

	index := loadRootsGolden(t, rootsGoldenV1)
	require.Len(t, index, len(lines), "every frozen line must load, not merely most of them")

	for _, e := range index {
		require.False(t, e.Root.Hash.IsZero(), "a loaded record must carry its root hash")
		require.NotEmpty(t, e.Root.Chunks, "every SP-06 record is chunked")
		require.True(t, e.Base.IsZero(), "no v=1 record declares a base")
		require.True(t, e.Orig.IsZero(), "nor a retained original")
		require.NotZero(t, e.Sig.Perms, "and every one of them carries its MinHash signature")
	}

	requireRootsGoldenRoundTrips(t, rootsGoldenV1)
}

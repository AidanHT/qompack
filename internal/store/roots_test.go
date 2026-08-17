package store

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
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

// TestEncodeSignature_OmittedWhileSketchIsAStub asserts an unmarshalable signature is omitted
// rather than failing the Put. internal/sketch's MarshalBinary reports ErrNotImplemented on this
// branch (Rule W-2), and near-duplicate detection being unavailable must never block ingest.
func TestEncodeSignature_OmittedWhileSketchIsAStub(t *testing.T) {
	_, ok := encodeSignature(sketch.Signature{})
	require.False(t, ok, "an empty signature has nothing to record")

	_, ok = encodeSignature(sketch.Signature{Perms: 128, Mins: []uint64{1, 2, 3}})
	require.False(t, ok, "while sketch.MarshalBinary is a stub, the signature is simply omitted")
}

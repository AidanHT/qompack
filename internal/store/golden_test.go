package store

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
)

// This file pins the on-disk shape of every index file this package writes.
//
// These goldens are SP-06's own artifacts and live under testdata/golden/store/. They are NOT the
// frozen fixtures under testdata/golden/contracts/store/, which pin something different: the
// encoding/json shape of the Go types Root, ToolUseRecord and Segment. internal/store/
// fixture_test.go covers those, and nothing here may touch them. The two coexist deliberately —
// index/roots.jsonl is a compact {"v":1,…} record carrying attribution and near-dup metadata that
// the Root struct does not have, while the contract fixture is the struct itself.
//
// WHAT IS MISSING FROM THESE GOLDENS, AND WHY (read before regenerating):
//
// No "sig" key appears in any roots.jsonl or tool_use.jsonl line. internal/sketch is still an
// SP-01 stub on this branch (Rule W-2): sketch.Signature.MarshalBinary reports
// core.ErrNotImplemented, and both writers treat a marshal failure as "no signature" and omit the
// key rather than failing the write. When SP-03 lands at the V2 checkpoint, real signatures will
// start being emitted and THESE GOLDENS WILL LEGITIMATELY NEED REGENERATING with -update. That is
// expected, not a regression — but a reviewer must be able to tell that from this comment rather
// than rediscovering it from a diff.
//
// Likewise internal/canon is a stub, so canonicalization is a no-op here and "canon" always equals
// "raw"; and internal/chunk is a stub, so the chunker is injected (cdcChunker) to produce a
// realistic multi-chunk array instead of splitChecked's single whole-input fallback.

// goldenUpdate reads the repository-wide -update flag, registering it if this test binary has not
// already. internal/testutil owns the canonical registration, but package store's in-package tests
// cannot import testutil (testutil imports store, so it would be an import cycle), and the lookup
// half of the same pattern keeps the two from ever colliding.
var goldenUpdate = registerGoldenUpdateFlag()

// registerGoldenUpdateFlag returns a reader for -update, adopting an existing registration when
// one is present. See internal/testutil/golden.go, whose behaviour this mirrors exactly.
func registerGoldenUpdateFlag() func() bool {
	const name = "update"
	if f := flag.Lookup(name); f != nil {
		return func() bool { return f.Value.String() == "true" }
	}
	p := flag.Bool(name, false, "rewrite golden files under testdata/golden/ instead of comparing against them")
	return func() bool { return *p }
}

// goldenStorePerm and goldenStoreDirPerm match internal/testutil's: a golden is committed source,
// not runtime state, so it is an ordinary readable file rather than the store's own 0o600.
const (
	goldenStorePerm    = 0o644
	goldenStoreDirPerm = 0o755
)

// goldenIndexFile compares one index file's bytes against testdata/golden/store/<name>, or rewrites
// it under -update. Comparison normalizes CRLF on both sides for the same reason testutil.Golden
// does: a Windows checkout can hand back CRLF for a .jsonl or .json golden, and failing on that
// would have nothing to do with the code under test.
func goldenIndexFile(t *testing.T, name string, got []byte) {
	t.Helper()

	p := filepath.Join("..", "..", "testdata", "golden", "store", name)
	gotLF := bytes.ReplaceAll(got, []byte("\r\n"), []byte("\n"))

	if goldenUpdate() {
		require.NoError(t, os.MkdirAll(filepath.Dir(p), goldenStoreDirPerm))
		require.NoError(t, os.WriteFile(p, gotLF, goldenStorePerm))
		return
	}

	want, err := os.ReadFile(p)
	require.NoError(t, err, "golden %s is missing; rerun with -update to create it", p)
	wantLF := bytes.ReplaceAll(want, []byte("\r\n"), []byte("\n"))
	if bytes.Equal(wantLF, gotLF) {
		return
	}
	t.Errorf("golden %s does not match (-want +got):\n%s", p, cmp.Diff(string(wantLF), string(gotLF)))
}

// goldenSessionIDs are the session and tool_use identifiers the scripted session uses. They are
// fixed strings rather than generated ones because they appear verbatim in the goldens.
const (
	goldenSession   = core.SessionID("sess_golden_sp06")
	goldenToolUseA  = core.ToolUseID("toolu_01GOLDENAAAAAAAAAAAAAAAA")
	goldenToolUseB  = core.ToolUseID("toolu_01GOLDENBBBBBBBBBBBBBBBB")
	goldenAuthPath  = "src/auth.ts"
	goldenCheckSeq  = core.CheckpointSeq(7)
	goldenStartTurn = core.TurnIndex(40)
	goldenEndTurn   = core.TurnIndex(57)
)

// goldenTick is how far the fake clock advances between scripted steps, so every record carries a
// distinct, reproducible timestamp.
const goldenTick = 250 * time.Millisecond

// TestGolden_IndexFormats runs one scripted session against a real store and pins all five index
// files byte-for-byte.
//
// Everything that reaches an index line is deterministic by construction: timestamps come from the
// package's fakeClock (frozen at testEpoch and advanced explicitly), hashes come from real content
// through an injected content-defined chunker, and the ids above are literals. Nothing reads a wall
// clock or a random source — the only randomness in the write path is the object staging filename,
// which is renamed away and never appears in an index.
func TestGolden_IndexFormats(t *testing.T) {
	tp := newTestStore(t)
	ctx := context.Background()
	clk := tp.Clock

	// 1. A plain put with no path, attributed to Bash.
	bashOut := []byte("go test ./internal/auth/...\nok  \tgithub.com/qompack/qompack/internal/auth\t0.184s\n")
	first, err := tp.Store.PutBytes(ctx, bashOut, PutOptions{Tool: "Bash"})
	require.NoError(t, err)
	clk.Advance(goldenTick)

	// 2. A put on a path, attributed to FileRead, large enough to span several chunks.
	authV1 := fixture(t, "fileread-auth-v1.txt")
	second, err := tp.Store.PutBytes(ctx, authV1, PutOptions{Tool: "FileRead", Path: goldenAuthPath})
	require.NoError(t, err)
	require.Greater(t, len(second.Root.Chunks), 1, "fixture sanity: the golden roots line must carry a multi-chunk array")
	clk.Advance(goldenTick)

	// 3. Two tool_use records: the second supersedes the first on the same path.
	argsA, previewA := ArgsDigest(json.RawMessage(`{"file_path":"src/auth.ts","limit":200,"offset":0}`))
	require.NoError(t, tp.Store.RecordToolUse(ctx, ToolUseRecord{
		ID: goldenToolUseA, Session: goldenSession, Turn: 41, TS: core.UnixMilli(clk.Now().UnixMilli()),
		Tool: "FileRead", ArgsDigest: argsA, ArgsPreview: previewA,
		Root: second.Root.Hash, Path: goldenAuthPath,
		Bytes: second.Root.RawBytes, Tokens: second.Root.Tokens,
	}))
	clk.Advance(goldenTick)

	argsB, previewB := ArgsDigest(json.RawMessage(`{"command":"go test ./internal/auth/..."}`))
	require.NoError(t, tp.Store.RecordToolUse(ctx, ToolUseRecord{
		ID: goldenToolUseB, Session: goldenSession, Turn: 42, TS: core.UnixMilli(clk.Now().UnixMilli()),
		Tool: "Bash", ArgsDigest: argsB, ArgsPreview: previewB,
		Root: first.Root.Hash, Path: goldenAuthPath,
		Bytes: first.Root.RawBytes, Tokens: first.Root.Tokens,
	}))
	clk.Advance(goldenTick)

	require.NoError(t, tp.Store.MarkSuperseded(ctx, goldenToolUseA, goldenToolUseB))
	clk.Advance(goldenTick)

	// 4. Two file versions for the same path, so files.json carries an ordered history.
	require.NoError(t, tp.Store.AppendFileVersion(ctx, goldenAuthPath, FileVersion{
		TS: core.UnixMilli(clk.Now().UnixMilli()), Root: second.Root.Hash, Turn: 41, Bytes: second.Root.RawBytes,
	}))
	clk.Advance(goldenTick)

	authV2 := fixture(t, "fileread-auth-v2.txt")
	third, err := tp.Store.PutBytes(ctx, authV2, PutOptions{Tool: "FileRead", Path: goldenAuthPath})
	require.NoError(t, err)
	clk.Advance(goldenTick)
	require.NoError(t, tp.Store.AppendFileVersion(ctx, goldenAuthPath, FileVersion{
		TS: core.UnixMilli(clk.Now().UnixMilli()), Root: third.Root.Hash, Turn: 43, Bytes: third.Root.RawBytes,
	}))
	clk.Advance(goldenTick)

	// 5. One segment through its whole lifecycle: opened, closed with a feature summary and a
	// token count, then encoded into checkpoint 7.
	segID, err := tp.Store.Segments().Open(ctx, Segment{Session: goldenSession, StartTurn: goldenStartTurn})
	require.NoError(t, err)
	clk.Advance(goldenTick)
	require.NoError(t, tp.Store.Segments().Close(ctx, segID, goldenEndTurn, map[string]float64{
		"path_jaccard": 0.42, "tool_shift": 0.19, "gap_seconds": 0.03, "todo_transition": 1, "tokens": 18402,
	}))
	clk.Advance(goldenTick)
	require.NoError(t, tp.Store.Segments().MarkEncoded(ctx, []core.SegmentID{segID}, goldenCheckSeq))
	clk.Advance(goldenTick)

	// 6. Flush materializes files.json and appends the session record.
	require.NoError(t, tp.Store.Flush(ctx))

	index := paths.Of(tp.Root).Index
	for _, name := range []string{rootsFile, toolUseFile, segmentsFile, filesViewNam, sessionsFile} {
		b, readErr := os.ReadFile(paths.Long(filepath.Join(index, name)))
		require.NoError(t, readErr, "index file %s was never written", name)
		goldenIndexFile(t, name, b)
	}
}

// TestGolden_IndexFormatsAreStableWithinAProcess asserts the scripted session produces
// byte-identical index files on a second, independent run in the same process.
//
// TestGolden_IndexFormats under -count=2 already re-runs the body, but it compares each run against
// the committed file rather than against the previous run. This compares two live runs directly, so
// a source of nondeterminism that happened to be baked into the golden — map iteration order
// captured at -update time, say — is caught here rather than silently frozen.
func TestGolden_IndexFormatsAreStableWithinAProcess(t *testing.T) {
	run := func(t *testing.T) map[string][]byte {
		t.Helper()
		tp := newTestStore(t)
		ctx := context.Background()
		clk := tp.Clock

		payload := []byte("deterministic golden stability payload\n")
		res, err := tp.Store.PutBytes(ctx, payload, PutOptions{Tool: "FileRead", Path: goldenAuthPath})
		require.NoError(t, err)
		clk.Advance(goldenTick)

		args, preview := ArgsDigest(json.RawMessage(`{"file_path":"src/auth.ts"}`))
		require.NoError(t, tp.Store.RecordToolUse(ctx, ToolUseRecord{
			ID: goldenToolUseA, Session: goldenSession, Turn: 1, TS: core.UnixMilli(clk.Now().UnixMilli()),
			Tool: "FileRead", ArgsDigest: args, ArgsPreview: preview,
			Root: res.Root.Hash, Path: goldenAuthPath, Bytes: res.Root.RawBytes, Tokens: res.Root.Tokens,
		}))
		clk.Advance(goldenTick)
		require.NoError(t, tp.Store.AppendFileVersion(ctx, goldenAuthPath, FileVersion{
			TS: core.UnixMilli(clk.Now().UnixMilli()), Root: res.Root.Hash, Turn: 1, Bytes: res.Root.RawBytes,
		}))
		require.NoError(t, tp.Store.Flush(ctx))

		index := paths.Of(tp.Root).Index
		out := map[string][]byte{}
		for _, name := range []string{rootsFile, toolUseFile, filesViewNam, sessionsFile} {
			b, readErr := os.ReadFile(paths.Long(filepath.Join(index, name)))
			require.NoError(t, readErr)
			out[name] = b
		}
		return out
	}

	first := run(t)
	second := run(t)
	for name, want := range first {
		require.Equal(t, string(want), string(second[name]),
			"%s differs between two identical scripted runs in one process", name)
	}
}

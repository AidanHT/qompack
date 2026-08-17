package store

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/obs"
	"github.com/qompack/qompack/internal/paths"
)

// idxEpoch is the instant every index test's clock starts at, matching testutil.Epoch so a golden
// timestamp written here reads the same as one written anywhere else in the tree.
//
// These tests live in `package store` rather than `package store_test` because they exercise
// openFS directly: store.Open still returns the SP-01 stub until the integration commit flips it,
// and internal/testutil imports internal/store, so an internal test file cannot reach testutil
// without an import cycle. Hence the small local clock below instead of testutil.FakeClock.
var idxEpoch = time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)

// idxClock is a core.Clock that only moves when a test moves it.
type idxClock struct {
	mu sync.Mutex
	t  time.Time
}

func newIdxClock() *idxClock { return &idxClock{t: idxEpoch} }

func (c *idxClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *idxClock) Since(t time.Time) time.Duration { return c.Now().Sub(t) }

func (c *idxClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// idxFixture is one opened store plus the seams a test needs to drive and observe it.
type idxFixture struct {
	s    *FSStore
	root string
	clk  *idxClock
	reg  obs.Registry
}

// newIdxStore opens a fresh store under t.TempDir().
func newIdxStore(t *testing.T) *idxFixture {
	t.Helper()
	root := t.TempDir()
	clk := newIdxClock()
	reg := obs.New(clk)
	s, err := openFS(root, config.Defaults(), Deps{Log: logging.Nop(), Clock: clk, Metrics: reg})
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	return &idxFixture{s: s, root: root, clk: clk, reg: reg}
}

// reopen closes the store and opens a new one over the same project root, which is how every
// "survives a restart" assertion in this file proves the append-only log is the truth.
func (f *idxFixture) reopen(t *testing.T) *FSStore {
	t.Helper()
	require.NoError(t, f.s.Close())
	s, err := openFS(f.root, config.Defaults(), Deps{Log: logging.Nop(), Clock: f.clk, Metrics: f.reg})
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	f.s = s
	return s
}

// indexLines returns every non-empty line of one index file under <root>/.qompack/index.
func indexLines(t *testing.T, root, name string) [][]byte {
	t.Helper()
	b, err := os.ReadFile(paths.Long(filepath.Join(paths.Of(root).Index, name)))
	if os.IsNotExist(err) {
		return nil
	}
	require.NoError(t, err)
	var out [][]byte
	for _, line := range bytes.Split(b, []byte{'\n'}) {
		if len(bytes.TrimSpace(line)) > 0 {
			out = append(out, bytes.TrimSpace(line))
		}
	}
	return out
}

// sampleToolUse builds a fully-populated record, so a round-trip test actually exercises every
// field rather than only the ones that happen to be non-zero.
func sampleToolUse(id core.ToolUseID, path string, ts core.UnixMilli) ToolUseRecord {
	digest, preview := ArgsDigest(json.RawMessage(`{"file_path":"` + path + `","limit":200}`))
	return ToolUseRecord{
		ID: id, Session: "sess-7f", Turn: 42, TS: ts, Tool: "FileRead",
		ArgsDigest: digest, ArgsPreview: preview,
		Root:  core.HashBytes(core.DomainRoot, []byte(string(id))),
		Path:  path,
		Bytes: 24188, Tokens: 5942,
		Status: StatusOK, Ephemeral: false, Subagent: "",
	}
}

func TestRecordToolUse_Roundtrip(t *testing.T) {
	f := newIdxStore(t)
	ctx := context.Background()

	want := sampleToolUse("toolu_01ABC", "src/auth.ts", 1734128400123)
	require.NoError(t, f.s.RecordToolUse(ctx, want))

	got, err := f.s.ToolUse(ctx, want.ID)
	require.NoError(t, err)
	require.Equal(t, want, got)

	// The record must survive a restart, because index/tool_use.jsonl — not memory — is the truth.
	s2 := f.reopen(t)
	got, err = s2.ToolUse(ctx, want.ID)
	require.NoError(t, err)
	require.Equal(t, want, got)
}

func TestRecordToolUse_IdempotentReplay(t *testing.T) {
	f := newIdxStore(t)
	ctx := context.Background()

	rec := sampleToolUse("toolu_01ABC", "src/auth.ts", 1734128400123)
	require.NoError(t, f.s.RecordToolUse(ctx, rec))
	require.NoError(t, f.s.RecordToolUse(ctx, rec), "replaying an identical record must be a silent no-op")

	require.Len(t, indexLines(t, f.root, toolUseFile), 1,
		"a replayed record must not append a second line: a daemon WAL replay after a crash would otherwise grow the index without bound")
}

func TestRecordToolUse_ConflictingReplay(t *testing.T) {
	f := newIdxStore(t)
	ctx := context.Background()

	rec := sampleToolUse("toolu_01ABC", "src/auth.ts", 1734128400123)
	require.NoError(t, f.s.RecordToolUse(ctx, rec))

	conflicting := rec
	conflicting.Root = core.HashBytes(core.DomainRoot, []byte("a different result"))
	err := f.s.RecordToolUse(ctx, conflicting)
	require.ErrorIs(t, err, core.ErrAppendOnly,
		"one tool_use id names one tool call, and one tool call has one result")
	require.Len(t, indexLines(t, f.root, toolUseFile), 1)
}

func TestToolUsesByPath_Ordering(t *testing.T) {
	f := newIdxStore(t)
	ctx := context.Background()
	const p = "src/auth.ts"

	ids := []core.ToolUseID{"toolu_1", "toolu_2", "toolu_3", "toolu_4", "toolu_5"}
	for i, id := range ids {
		require.NoError(t, f.s.RecordToolUse(ctx, sampleToolUse(id, p, core.UnixMilli(1000+i))))
	}

	got, err := f.s.ToolUsesByPath(ctx, p, 3)
	require.NoError(t, err)
	require.Len(t, got, 3)
	require.Equal(t, core.ToolUseID("toolu_5"), got[0].ID, "newest first")
	require.Equal(t, core.ToolUseID("toolu_4"), got[1].ID)
	require.Equal(t, core.ToolUseID("toolu_3"), got[2].ID)

	all, err := f.s.ToolUsesByPath(ctx, p, 0)
	require.NoError(t, err)
	require.Len(t, all, 5, "a limit of zero or less means every record")
}

// TestMarkSuperseded_AppendOnly is the append-only half of supersession: the original line is
// never rewritten, and the status change arrives as an additional record (§7.4).
func TestMarkSuperseded_AppendOnly(t *testing.T) {
	f := newIdxStore(t)
	ctx := context.Background()

	older := sampleToolUse("toolu_01ABC", "src/auth.ts", 1000)
	newer := sampleToolUse("toolu_01XYZ", "src/auth.ts", 2000)
	require.NoError(t, f.s.RecordToolUse(ctx, older))
	require.NoError(t, f.s.RecordToolUse(ctx, newer))

	before := indexLines(t, f.root, toolUseFile)
	require.Len(t, before, 2)
	originalLine := append([]byte(nil), before[0]...)

	require.NoError(t, f.s.MarkSuperseded(ctx, older.ID, newer.ID))
	require.NoError(t, f.s.MarkSuperseded(ctx, older.ID, newer.ID), "marking the same pair twice is a no-op")

	after := indexLines(t, f.root, toolUseFile)
	require.Len(t, after, 3, "exactly one supersede record, and the idempotent second call writes nothing")
	require.Equal(t, originalLine, after[0], "the original record line must be byte-for-byte unmodified")

	var mut struct {
		Op string         `json:"op"`
		ID core.ToolUseID `json:"id"`
		By core.ToolUseID `json:"by"`
	}
	require.NoError(t, json.Unmarshal(after[2], &mut))
	require.Equal(t, "supersede", mut.Op)
	require.Equal(t, older.ID, mut.ID)
	require.Equal(t, newer.ID, mut.By)

	// The mutation must be replayed on reopen, last-wins.
	s2 := f.reopen(t)
	got, err := s2.ToolUse(ctx, older.ID)
	require.NoError(t, err)
	require.Equal(t, StatusSuperseded, got.Status)
	require.Equal(t, newer.ID, got.SupersededBy)
}

func TestMarkSuperseded_Unknown(t *testing.T) {
	f := newIdxStore(t)
	ctx := context.Background()

	known := sampleToolUse("toolu_known", "src/auth.ts", 1000)
	require.NoError(t, f.s.RecordToolUse(ctx, known))

	require.ErrorIs(t, f.s.MarkSuperseded(ctx, "toolu_missing", known.ID), core.ErrNotFound)
	require.ErrorIs(t, f.s.MarkSuperseded(ctx, known.ID, "toolu_missing"), core.ErrNotFound)
	require.Len(t, indexLines(t, f.root, toolUseFile), 1, "a refused supersede writes nothing")
}

func TestArgsDigest_KeyOrderInvariant(t *testing.T) {
	a, pa := ArgsDigest(json.RawMessage(`{"a":1,"b":2}`))
	b, pb := ArgsDigest(json.RawMessage(`{"b":2,"a":1}`))
	require.Equal(t, a, b, "object key order must not change the digest")
	require.Equal(t, pa, pb)

	// Array order, by contrast, IS significant: [1,2] and [2,1] are different arguments.
	c, _ := ArgsDigest(json.RawMessage(`{"xs":[1,2]}`))
	d, _ := ArgsDigest(json.RawMessage(`{"xs":[2,1]}`))
	require.NotEqual(t, c, d, "array order is significant and must change the digest")

	// Whitespace is insignificant.
	e, _ := ArgsDigest(json.RawMessage("{\n  \"a\" : 1,\n  \"b\":2\n}"))
	require.Equal(t, a, e)
}

func TestArgsDigest_Preview(t *testing.T) {
	_, preview := ArgsDigest(json.RawMessage(`{"file_path":"src/auth.ts","limit":200}`))
	require.Equal(t, "src/auth.ts", preview)

	long := strings.Repeat("echo hello world; ", 40) // well over 400 characters
	_, preview = ArgsDigest(json.RawMessage(`{"command":` + mustJSON(t, long) + `}`))
	require.LessOrEqual(t, len(preview), argsPreviewMax, "preview must fit the ≤120-byte budget")
	require.True(t, strings.HasSuffix(preview, previewEllipsis), "a truncated preview ends in an ellipsis")
	require.True(t, utf8Valid(preview), "truncation must land on a rune boundary")

	// A document with no identifying key falls back to the canonical JSON.
	_, preview = ArgsDigest(json.RawMessage(`{"limit":200}`))
	require.Equal(t, `{"limit":200}`, preview)

	// Control characters are stripped and whitespace runs collapse. The BEL is built programmatically
	// and marshalled by encoding/json, which escapes it properly: RFC 8259 forbids a RAW control
	// character inside a string, so a literal one would be malformed and take the fallback path.
	_, preview = ArgsDigest(mustMarshal(t, map[string]any{"command": "a" + string(rune(7)) + "b   c"}))
	require.Equal(t, "ab c", preview)
}

// TestArgsDigest_MalformedInputIsStillStable pins the fallback: an unparseable tool_input is
// digested over its raw bytes rather than collapsing to one shared empty digest, so two different
// malformed argument documents never look like the same tool call.
func TestArgsDigest_MalformedInputIsStillStable(t *testing.T) {
	bad1 := json.RawMessage(`{"command":`)
	bad2 := json.RawMessage(`{"command":"unterminated`)

	h1, p1 := ArgsDigest(bad1)
	h1Again, _ := ArgsDigest(bad1)
	h2, _ := ArgsDigest(bad2)

	require.Equal(t, h1, h1Again, "digesting the same malformed input twice must be stable")
	require.NotEqual(t, h1, h2, "two different malformed inputs must not collide")
	require.NotEmpty(t, p1)

	// Empty input is the one case that legitimately digests to the domain's empty digest.
	hEmpty, pEmpty := ArgsDigest(json.RawMessage(`   `))
	require.Equal(t, core.HashBytes(core.DomainArgs, nil), hEmpty)
	require.Empty(t, pEmpty)
}

// TestArgsDigest_LargeIntegerIsNotCorrupted is what json.Decoder.UseNumber buys: a byte offset or
// an epoch-nanosecond timestamp above 2^53 must survive canonicalization exactly, or two tool
// calls that differ only in such a value would collide on one digest.
func TestArgsDigest_LargeIntegerIsNotCorrupted(t *testing.T) {
	a, _ := ArgsDigest(json.RawMessage(`{"offset":9007199254740993}`))
	b, _ := ArgsDigest(json.RawMessage(`{"offset":9007199254740992}`))
	require.NotEqual(t, a, b,
		"9007199254740993 and 9007199254740992 are distinct arguments; routing them through float64 would collapse both to 9007199254740992")

	_, preview := ArgsDigest(json.RawMessage(`{"offset":9007199254740993}`))
	require.Equal(t, `{"offset":9007199254740993}`, preview, "the number must be re-emitted verbatim")

	// A high-precision decimal must likewise survive unrounded.
	_, preview = ArgsDigest(json.RawMessage(`{"ratio":0.1000000000000000055511151231257827}`))
	require.Contains(t, preview, "0.1000000000000000055511151231257827")
}

// mustJSON renders s as a JSON string literal.
func mustJSON(t *testing.T, s string) string {
	t.Helper()
	b, err := json.Marshal(s)
	require.NoError(t, err)
	return string(b)
}

// utf8Valid reports whether s is well-formed UTF-8.
func utf8Valid(s string) bool {
	for _, r := range s {
		if r == '�' {
			return false
		}
	}
	return true
}

// mustMarshal renders v as compact JSON. Fixtures that must carry a control character are built
// through it rather than written as a source-level escape, so encoding/json — not the source
// encoding — decides how the character reaches the parser.
func mustMarshal(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return json.RawMessage(b)
}
